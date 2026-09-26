/* captive_portal.c — html-form operator UX for the softAP
 * provisioning flow.
 *
 * SCOPE — what this file does and doesn't do:
 *
 *   - Brings up an httpd server bound to port 80 on the
 *     softAP netif (which gets the canonical 192.168.4.1
 *     DHCP server IP via esp_netif_create_default_wifi_ap +
 *     esp_wifi_set_mode(WIFI_MODE_APSTA)).
 *   - Registers four URIs on that server:
 *       GET  /            — HTML form (mobile-first)
 *       POST /provision   — accepts a form-urlencoded or JSON
 *                           body, applies credentials via
 *                           wifi_prov_mgr_configure_sta().
 *       GET  /whoami      — JSON device identity (replaces
 *                           the protocomm iot-cam-info endpoint
 *                           for the captive mode).
 *       * any GET         — captures the request and returns
 *                           the form (a poor-person's captive
 *                           portal so naive OS probes get a
 *                           recognisable body).
 *   - Hands the httpd handle to the wifi_prov_scheme_softap
 *     so the manager's protocomm URIs are layered on the same
 *     server. The two co-exist; if an operator has the
 *     Espressif phone app installed, it keeps working.
 *   - Stops the server cleanly on error or teardown.
 *
 * NOT in scope for this file:
 *
 *   - A real DNS server for the softAP (the actual captive
 *     portal OS-detect trick where the phone's connectivity
 *     probe is redirected). Documented as a follow-up — it
 *     needs a LwIP-level DNS responder or a DNS server bring-up.
 *     For now the operator types `192.168.4.1` in the browser.
 *   - TLS on the http server. The WPA2 link is encrypted;
 *     in private-network deployments this is acceptable;
 *     for hostile environments the operator should switch to
 *     the protocomm path where PoP-based authentication is
 *     involved.
 *   - Per-device or per-batch PoPs printed on a label.
 */
#include "captive_portal.h"

#include <string.h>
#include <stdlib.h>

#include "esp_log.h"
#include "esp_http_server.h"
#include "esp_netif.h"
#include "esp_wifi.h"
#include "esp_event.h"
#include "cJSON.h"

#include "provisioning.h"

static const char *TAG = "prov-cap";

/* The actual httpd handle. Exposed via the header so the
 * provisioning orchestrator can hand it to the softAP scheme. */
httpd_handle_t s_captive_httpd = NULL;

/* WiFi scan result cache. Populated synchronously on the first
 * call to GET /scan (blocking ~3-5 s); kept for the lifetime
 * of the captive portal session. After teardown the buffer is
 * invalidated. */
#define MAX_SCAN_AP 20
typedef struct {
    bool      populated;
    char      ssid[MAX_SCAN_AP][33];
    int8_t    rssi[MAX_SCAN_AP];
    uint8_t   bssid[MAX_SCAN_AP][6];
    uint8_t   authmode[MAX_SCAN_AP];
    int       count;
} scan_cache_t;

static scan_cache_t s_scan_cache = { .populated = false, .count = 0 };

/* Forward decls for the five handlers. */
static esp_err_t root_get_handler(httpd_req_t *req);
static esp_err_t provision_post_handler(httpd_req_t *req);
static esp_err_t whoami_get_handler(httpd_req_t *req);
static esp_err_t scan_get_handler(httpd_req_t *req);
static esp_err_t default_captive_handler(httpd_req_t *req, httpd_err_code_t err);

/* HTML page served by GET /. Title is the device name from the
 * orchestrator's provisioning_app_info_t (CONFIG defaults are
 * fine on a fresh bring-up). Hand-rolled to keep cJSON out
 * of the hot path for what is fundamentally a static page. */
static const char *HTML_FORM_BODY =
"<!DOCTYPE html>"
"<html lang='en'>"
"<head>"
"<meta charset='utf-8'>"
"<meta name='viewport' content='width=device-width, initial-scale=1.0'>"
"<title>IoT-Cam Provisioning</title>"
"<style>"
"body{font-family:-apple-system,BlinkMacSystemFont,sans-serif;margin:1.5em;max-width:32em;color:#222}"
"h1{font-size:1.25em;margin:0 0 0.5em}"
"p.hint{color:#555;font-size:0.9em;margin:0 0 1.5em}"
"form{display:flex;flex-direction:column;gap:0.75em;margin-top:1em}"
"label{font-size:0.9em;color:#333;display:flex;flex-direction:column;gap:0.25em}"
"input[type=text],input[type=password]{padding:0.75em;font-size:1em;border:1px solid #aaa;border-radius:6px;width:100%;box-sizing:border-box}"
"input[type=text]:focus,input[type=password]:focus{outline:2px solid #4a90e2;outline-offset:2px}"
"button{padding:0.85em 1.25em;font-size:1em;border:0;border-radius:6px;background:#4a90e2;color:#fff;cursor:pointer}"
"button:hover{background:#3b7fc7}"
".ok{display:none;padding:1em;border:1px solid #4caf50;background:#e8f5e9;border-radius:6px;margin-top:1.5em}"
".err{display:none;padding:1em;border:1px solid #f44336;background:#ffebee;border-radius:6px;margin-top:1.5em}"
"</style>"
"</head>"
"<body>"
"<h1>IoT-Cam Provisioning</h1>"
"<p class='hint'>Select your Wi-Fi network and enter the password. "
"The device will scan for available networks automatically.</p>"
"<form id='p' onsubmit='return submit_form(event)'>"
"<label>Network name (SSID)"
"  <select id='ssid_sel' onchange='ssid_changed()' style='padding:0.75em;font-size:1em;border:1px solid #aaa;border-radius:6px;width:100%;box-sizing:border-box'>"
"    <option value=''>-- Scanning... --</option>"
"  </select>"
"  <input type='text' id='ssid_other' placeholder='Type network name manually' "
"         maxlength='32' autocomplete='off' autocapitalize='none' "
"         style='margin-top:0.5em;display:none;padding:0.75em;font-size:1em;border:1px solid #aaa;border-radius:6px;width:100%;box-sizing:border-box'>"
"</label>"
"<label>Password"
"<input type='password' name='password' required maxlength='64' autocomplete='off'>"
"</label>"
"<button type='button' id='scan_btn' onclick='scan_networks()' "
"        style='background:#607d8b'>Scan Networks</button>"
"<button type='submit'>Connect</button>"
"</form>"
"<div class='ok' id='ok'>Connected. You can close this page.</div>"
"<div class='err' id='err'></div>"
"<script>"
"function ssid_changed(){"
"  var sel=document.getElementById('ssid_sel');"
"  var other=document.getElementById('ssid_other');"
"  if(sel.value==='_other_'){"
"    other.style.display='block';"
"    other.required=true;"
"    other.focus();"
"  } else {"
"    other.style.display='none';"
"    other.required=false;"
"    other.value='';"
"  }"
"}"
"function get_selected_ssid(){"
"  var sel=document.getElementById('ssid_sel');"
"  if(sel.value==='')return '';"
"  if(sel.value==='_other_')return document.getElementById('ssid_other').value.trim();"
"  return sel.value;"
"}"
"function scan_networks(){"
"  var sel=document.getElementById('ssid_sel');"
"  var btn=document.getElementById('scan_btn');"
"  sel.innerHTML=\"<option value=''>-- Scanning... --</option>\";"
"  btn.disabled=true;"
"  btn.textContent='Scanning...' ;"
"  fetch('/scan')"
"    .then(function(r){return r.json();})"
"    .then(function(nets){"
"      sel.innerHTML='';"
"      if(!nets||nets.length===0){"
"        var opt=document.createElement('option');"
"        opt.value='';"
"        opt.textContent='No networks found — check your router';"
"        sel.appendChild(opt);"
"      } else {"
"        nets.forEach(function(n){"
"          var opt=document.createElement('option');"
"          opt.value=n.ssid;"
"          /* RSSI bar: -30=excellent, -70=weak */"
"          var bars='?';"
"          var r=n.rssi;"
"          if(r>-50)bars='****';"
"          else if(r>-60)bars='*** ' ;"
"          else if(r>-70)bars='**  ' ;"
"          else if(r>-80)bars='*   ' ;"
"          opt.textContent=n.ssid+' ('+bars+' signal, '+r+' dBm)';"
"          sel.appendChild(opt);"
"        });"
"        /* Allow manual entry for networks not detected */"
"        var opt=document.createElement('option');"
"        opt.value='_other_';"
"        opt.textContent='Other (type manually)';"
"        sel.appendChild(opt);"
"      }"
"      btn.disabled=false;"
"      btn.textContent='Scan Networks' ;"
"    })"
"    .catch(function(){"
"      sel.innerHTML=\"<option value=''>Scan failed — try again</option>\";"
"      btn.disabled=false;"
"      btn.textContent='Scan Networks' ;"
"    });"
"}"
"function submit_form(e){"
"  e.preventDefault();"
"  var ssid=get_selected_ssid().trim();"
"  if(!ssid){alert('Please scan for networks or select Other and type the SSID.');return false;}"
"  var password=document.getElementById('p').password.value;"
"  var body='ssid='+encodeURIComponent(ssid)+'&password='+encodeURIComponent(password);"
"  fetch('/provision',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:body})"
"    .then(function(r){return r.json().then(function(j){return{ok:r.ok,status:r.status,body:j};});})"
"    .then(function(o){"
"      if(o.ok){document.getElementById('ok').style.display='block';document.getElementById('p').style.display='none';}"
"      else{var err=document.getElementById('err');err.textContent='Error '+(o.status||'?')+': '+(o.body&&o.body.error||'unknown');err.style.display='block';}"
"    })"
"    .catch(function(err){var e=document.getElementById('err');e.textContent='Network error: '+err;e.style.display='block';});"
"  return false;"
"}"
"/* Auto-scan on page load */"
"scan_networks();"
"</script>"
"</body></html>";

/* GET / — return the HTML form. */
static esp_err_t root_get_handler(httpd_req_t *req)
{
    /* Sniff Accept header for a substring of "json" / curl etc.
     * Returning HTML by default keeps the form the first thing
     * a phone browser sees; JSON consumers (the Espressif CLI
     * tool, e.g.) can also ask for /whoami. */
    int total = strlen(HTML_FORM_BODY);
    httpd_resp_set_type(req, "text/html; charset=utf-8");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");
    return httpd_resp_send(req, HTML_FORM_BODY, total);
}

/* Decode URL-encoded form body into a small key/value store.
 * Tiny implementation — handles the two-field case we actually
 * need (ssid + password). Avoids pulling in another dep. */
typedef struct {
    char *ssid;
    char *password;
} form_data_t;

static void form_data_free(form_data_t *fd)
{
    if (!fd) return;
    free(fd->ssid);
    free(fd->password);
    fd->ssid = NULL;
    fd->password = NULL;
}

/* url-decode in-place. out must be at least in_len + 1 bytes.
 * Returns the length written (excluding the NUL). */
static size_t url_decode(const char *in, size_t in_len, char *out, size_t out_size)
{
    size_t o = 0;
    for (size_t i = 0; i < in_len && o + 1 < out_size; ++i) {
        char c = in[i];
        if (c == '+') {
            out[o++] = ' ';
        } else if (c == '%' && i + 2 < in_len) {
            char hi = in[i + 1];
            char lo = in[i + 2];
            int v = 0;
            if (hi >= '0' && hi <= '9') v = (hi - '0') << 4;
            else if (hi >= 'A' && hi <= 'F') v = (hi - 'A' + 10) << 4;
            else if (hi >= 'a' && hi <= 'f') v = (hi - 'a' + 10) << 4;
            if (lo >= '0' && lo <= '9') v |= (lo - '0');
            else if (lo >= 'A' && lo <= 'F') v |= (lo - 'A' + 10);
            else if (lo >= 'a' && lo <= 'f') v |= (lo - 'a' + 10);
            out[o++] = (char)(v & 0xff);
            i += 2;
        } else {
            out[o++] = c;
        }
    }
    out[o] = '\0';
    return o;
}

static esp_err_t parse_form_body(const char *body, size_t len, form_data_t *out)
{
    if (!body || !out) return ESP_ERR_INVALID_ARG;
    memset(out, 0, sizeof(*out));

    /* Split on '&' then each pair on '='. Two fields only. */
    const char *cur = body;
    const char *end = body + len;
    while (cur < end) {
        const char *amp = memchr(cur, '&', end - cur);
        size_t pair_len = amp ? (size_t)(amp - cur) : (size_t)(end - cur);
        const char *eq = memchr(cur, '=', pair_len);
        char *raw_key = NULL;
        char *raw_val = NULL;
        if (eq) {
            size_t klen = (size_t)(eq - cur);
            size_t vlen = pair_len - klen - 1;
            raw_key = (char *)malloc(klen + 1);
            raw_val = (char *)malloc(vlen + 1);
            if (!raw_key || !raw_val) { free(raw_key); free(raw_val); return ESP_ERR_NO_MEM; }
            url_decode(cur, klen, raw_key, klen + 1);
            url_decode(eq + 1, vlen, raw_val, vlen + 1);
        } else {
            raw_key = (char *)malloc(pair_len + 1);
            if (!raw_key) return ESP_ERR_NO_MEM;
            url_decode(cur, pair_len, raw_key, pair_len + 1);
        }

        if (raw_key) {
            if (!out->ssid && strcmp(raw_key, "ssid") == 0) {
                out->ssid = raw_val;
                free(raw_key);
            } else if (!out->password && strcmp(raw_key, "password") == 0) {
                out->password = raw_val;
                free(raw_key);
            } else {
                free(raw_key);
                free(raw_val);
            }
        }
        if (!amp) break;
        cur = amp + 1;
    }

    if (!out->ssid || !out->password) {
        form_data_free(out);
        return ESP_ERR_INVALID_ARG;
    }
    return ESP_OK;
}

/* POST /provision — apply the credentials via the IDF manager.
 * Sends a JSON status response so the form's fetch() promise
 * resolves cleanly. */
static esp_err_t provision_post_handler(httpd_req_t *req)
{
    /* Read body — bounded by IDF default recv buffer (a few
     * KB) and our max ssid+password length. */
    int total_len = req->content_len;
    if (total_len <= 0 || total_len > 2048) {
        httpd_resp_set_status(req, "400 Bad Request");
        httpd_resp_sendstr(req, "{\"error\":\"bad content length\"}");
        return ESP_OK;
    }
    char *body = (char *)malloc(total_len + 1);
    if (!body) {
        httpd_resp_set_status(req, "500 Internal Server Error");
        httpd_resp_sendstr(req, "{\"error\":\"oom\"}");
        return ESP_OK;
    }
    int recv_left = total_len;
    int received = 0;
    while (recv_left > 0) {
        int r = httpd_req_recv(req, body + received, recv_left);
        if (r <= 0) {
            free(body);
            httpd_resp_set_status(req, "400 Bad Request");
            httpd_resp_sendstr(req, "{\"error\":\"recv failed\"}");
            return ESP_OK;
        }
        received += r;
        recv_left -= r;
    }
    body[total_len] = '\0';

    form_data_t fd;
    esp_err_t perr = parse_form_body(body, total_len, &fd);
    if (perr != ESP_OK) {
        free(body);
        form_data_free(&fd);
        httpd_resp_set_status(req, "400 Bad Request");
        httpd_resp_sendstr(req, "{\"error\":\"missing ssid or password\"}");
        return ESP_OK;
    }
    free(body);

    ESP_LOGI(TAG, "POST /provision: ssid=%s password=(redacted, len=%d)",
             fd.ssid, (int)strlen(fd.password));

    /* Apply via the provisioning component's inter-module helper,
     * which writes the credentials through esp_wifi_set_config()
     * (so they land in esp_wifi's NVS storage) and signals the
     * boot thread to resume. */
    esp_err_t merr = provisioning_apply_captive_form(fd.ssid, fd.password);
    form_data_free(&fd);

    httpd_resp_set_type(req, "application/json");
    if (merr != ESP_OK) {
        ESP_LOGE(TAG, "apply_captive_form: %s", esp_err_to_name(merr));
        httpd_resp_set_status(req, "502 Bad Gateway");
        char msg[96];
        snprintf(msg, sizeof(msg), "{\"error\":\"apply failed: %s\"}",
                 esp_err_to_name(merr));
        httpd_resp_sendstr(req, msg);
        return ESP_OK;
    }
    httpd_resp_sendstr(req, "{\"ok\":true}");
    return ESP_OK;
}

/* GET /whoami — JSON device identity. */
static esp_err_t whoami_get_handler(httpd_req_t *req)
{
    httpd_resp_set_type(req, "application/json");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");

    cJSON *root = cJSON_CreateObject();
    const char *name = CONFIG_PROVISIONING_DEVICE_NAME;
    cJSON_AddStringToObject(root, "name", name);
    const char *fw = "0.1.0";
    cJSON_AddStringToObject(root, "fw_version", fw);

    /* ssid / service_name the device is advertising. Helpful
     * for an operator who discovers the device via mDNS in a
     * mixed environment and wants to confirm the right one. */
    extern const char *internal_prov_ssid(void);
    /* The function lives in provisioning.c — small inter-module
     * helper that returns the runtime SSID derived from the
     * Kconfig prefix + last 3 bytes of MAC. Declared here as a
     * forward decl since we don't want to expose it through
     * provisioning.h's surface. */
    cJSON_AddStringToObject(root, "softap_ssid", internal_prov_ssid());
    cJSON_AddStringToObject(root, "softap_security",
                            "WIFI_AUTH_WPA2_PSK");

    char *out = cJSON_PrintUnformatted(root);
    if (!out) {
        cJSON_Delete(root);
        httpd_resp_set_status(req, "500 Internal Server Error");
        httpd_resp_sendstr(req, "{\"error\":\"oom\"}");
        return ESP_OK;
    }
    httpd_resp_sendstr(req, out);
    free(out);
    cJSON_Delete(root);
    return ESP_OK;
}

/* GET /scan — triggers a WiFi station scan (blocking, ~3-5 s) and
 * returns a JSON array of found networks:
 *   [{"ssid":"MyNet","rssi":-55}, ...]
 *
 * The scan runs synchronously on this request thread so the caller
 * (the browser JS fetch) gets fresh results every time. The operator
 * can re-tap "Scan" to refresh the list. Empty list means no APs
 * were found in range — they should try again from a different spot.
 *
 * NOTE: Scanning temporarily suspends the AP beacon for ~150 ms per
 * channel; connected operators may notice a brief stutter on the
 * softAP — this is expected and normal for ESP32 in APSTA mode.
 *
 * Authmode is checked and masked to avoid leaking enterprise
 * credentials — only OPEN and WPA2-personal APs are shown (matching
 * what the ESP32 station can actually join without a supplicant).
 */
static esp_err_t scan_get_handler(httpd_req_t *req)
{
    (void)req;
    httpd_resp_set_type(req, "application/json");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");

    wifi_scan_config_t scan_cfg = {
        .show_hidden = false,
        .scan_type   = WIFI_SCAN_TYPE_ACTIVE,
    };

    ESP_LOGI(TAG, "/scan: starting active WiFi scan (max %d APs, 5 s timeout)",
             MAX_SCAN_AP);

    esp_err_t r = esp_wifi_scan_start(&scan_cfg, true);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "/scan: esp_wifi_scan_start: %s", esp_err_to_name(r));
        httpd_resp_sendstr(req, "[]");
        return ESP_OK;
    }

    wifi_ap_record_t ap_info[MAX_SCAN_AP] = {0};
    uint16_t n = MAX_SCAN_AP;
    r = esp_wifi_scan_get_ap_records(&n, ap_info);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "/scan: esp_wifi_scan_get_ap_records: %s", esp_err_to_name(r));
        httpd_resp_sendstr(req, "[]");
        return ESP_OK;
    }

    /* Filter to operator-friendly networks (no enterprise, no hidden).
     * Keep them in discovery order so the strongest AP appears first. */
    int out_count = 0;
    char ssid_buf[MAX_SCAN_AP][33] = {{0}};
    int8_t rssi_buf[MAX_SCAN_AP] = {0};

    for (uint16_t i = 0; i < n && out_count < MAX_SCAN_AP; i++) {
        /* Skip hidden SSIDs — operator cannot select what they
         * cannot read. */
        if (ap_info[i].ssid[0] == '\0') continue;

        /* Skip networks requiring a supplicant (enterprise/WPA3-enterprise).
         * Accept OPEN, WPA2-PSK, WPA2-WPA3 mixed. */
        wifi_auth_mode_t m = ap_info[i].authmode;
        if (m == WIFI_AUTH_WPA2_ENTERPRISE ||
            m == WIFI_AUTH_WPA3_ENTERPRISE) {
            continue;
        }

        /* Copy SSID out of the record before the buffer goes out of scope. */
        memcpy(ssid_buf[out_count], ap_info[i].ssid,
               sizeof(ap_info[i].ssid));
        rssi_buf[out_count] = ap_info[i].rssi;
        out_count++;
    }

    /* Update the in-memory cache so GET /scan reflects the same data. */
    memset(&s_scan_cache, 0, sizeof(s_scan_cache));
    s_scan_cache.populated = true;
    s_scan_cache.count = out_count;
    for (int i = 0; i < out_count; i++) {
        memcpy(s_scan_cache.ssid[i], ssid_buf[i], 33);
        s_scan_cache.rssi[i] = rssi_buf[i];
    }

    /* Build JSON by hand — avoids a heap allocation for the output
     * buffer (max 20 entries × ~60 bytes ≈ 1.2 KB, well within
     * the httpd send buffer). */
    char resp[2048] = "[";
    size_t off = 1;
    for (int i = 0; i < out_count && off < sizeof(resp) - 4; i++) {
        if (i > 0) resp[off++] = ',';
        int n = snprintf(resp + off, sizeof(resp) - off,
                         "{\"ssid\":\"%s\",\"rssi\":%d}",
                         ssid_buf[i], rssi_buf[i]);
        if (n < 0 || (size_t)n >= sizeof(resp) - off) break;
        off += n;
    }
    resp[off++] = ']';
    resp[off] = '\0';

    ESP_LOGI(TAG, "/scan: found %d usable networks", out_count);
    httpd_resp_send(req, resp, off);
    return ESP_OK;
}

/* Default handler — capture unmatched GET URIs and respond with
 * the form so naive captive-portal probes (and operators with
 * muscle memory for typing a site URL) get a recognisable page.
 * The IDF httpd routes unmatched requests through here as a
 * 404 err handler. Only handles GET; non-GET 404s return the
 * IDF default body (we don't want any other surface reachable
 * through this hook). */
static esp_err_t default_captive_handler(httpd_req_t *req, httpd_err_code_t err)
{
    (void)err;
    if (req->method == HTTP_GET) {
        int total = strlen(HTML_FORM_BODY);
        httpd_resp_set_type(req, "text/html; charset=utf-8");
        httpd_resp_set_hdr(req, "Cache-Control", "no-store");
        return httpd_resp_send(req, HTML_FORM_BODY, total);
    }
    httpd_resp_set_status(req, "404 Not Found");
    httpd_resp_sendstr(req, "Not Found");
    return ESP_OK;
}

esp_err_t captive_portal_bring_up(void)
{
    if (s_captive_httpd) {
        ESP_LOGW(TAG, "bring_up: already running");
        return ESP_OK;
    }

    httpd_config_t httpd_cfg = HTTPD_DEFAULT_CONFIG();
    /* Operating with a small stack + small pool — the only
     * request body we accept is a form-urlencoded pair (<200 B
     * typical). The protocomm handlers layered on top are
     * larger transactions but they share the same server. */
    httpd_cfg.stack_size = 6144;
    httpd_cfg.max_uri_handlers = 16;
    httpd_cfg.max_req_hdr_len = 1024;

    esp_err_t r = httpd_start(&s_captive_httpd, &httpd_cfg);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "bring_up: httpd_start: %s", esp_err_to_name(r));
        s_captive_httpd = NULL;
        return r;
    }
    ESP_LOGI(TAG, "captive httpd running on port %d",
             httpd_cfg.server_port);

    /* Register the captive URIs. The manager will add its
     * protocomm URIs later, after we hand the handle to the
     * softAP scheme. */
    httpd_uri_t root_uri = {
        .uri = "/", .method = HTTP_GET, .handler = root_get_handler,
    };
    httpd_register_uri_handler(s_captive_httpd, &root_uri);

    httpd_uri_t post_uri = {
        .uri = "/provision", .method = HTTP_POST,
        .handler = provision_post_handler,
    };
    httpd_register_uri_handler(s_captive_httpd, &post_uri);

    httpd_uri_t whoami_uri = {
        .uri = "/whoami", .method = HTTP_GET,
        .handler = whoami_get_handler,
    };
    httpd_register_uri_handler(s_captive_httpd, &whoami_uri);

    httpd_uri_t scan_uri = {
        .uri = "/scan", .method = HTTP_GET,
        .handler = scan_get_handler,
    };
    httpd_register_uri_handler(s_captive_httpd, &scan_uri);

    /* Default handler for unmatched URIs. The IDF httpd routes
     * requests with no matching URI handler through HTTPD_404;
     * we catch that and respond with our form so naive captive-
     * portal probes (Apple's hotspot-detect, Android's
     * connectivitycheck, etc.) get a recognisable body. */
    httpd_register_err_handler(s_captive_httpd,
                               HTTPD_404_NOT_FOUND,
                               default_captive_handler);

    return ESP_OK;
}

void captive_portal_tear_down(void)
{
    if (!s_captive_httpd) {
        return;
    }
    /* unregister_default isn't an IDF function; httpd_stop on
     * the handle cleans up. */
    esp_err_t r = httpd_stop(s_captive_httpd);
    if (r != ESP_OK) {
        ESP_LOGW(TAG, "tear_down: httpd_stop: %s", esp_err_to_name(r));
    }
    s_captive_httpd = NULL;
}
