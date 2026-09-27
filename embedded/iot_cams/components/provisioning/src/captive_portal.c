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

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/semphr.h"

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

/* WiFi scan result cache. Written by the scan task (on the
 * wifi task context) and read by the HTTPD handler on the httpd
 * task context. Both sides are single-threaded so no lock is needed:
 * the task writes, gives the sema, the handler reads. */
#define MAX_SCAN_AP 20
typedef struct {
    bool      populated;
    char      ssid[MAX_SCAN_AP][33];
    int8_t    rssi[MAX_SCAN_AP];
    int       count;
} scan_cache_t;

static scan_cache_t s_scan_cache;

/* Scan synchronisation: binary semaphore. The HTTPD handler takes it
 * (blocks), the scan task gives it when results are ready.
 * Static objects so no heap allocation is needed. */
static StaticSemaphore_t s_scan_done_sema_buf;
static SemaphoreHandle_t s_scan_done_sema;



/* Forward declaration of the scan task entry point. */
static void wifi_scan_task(void *arg);

/* Forward decls for the five handlers. */
static esp_err_t root_get_handler(httpd_req_t *req);
static esp_err_t provision_post_handler(httpd_req_t *req);
static esp_err_t whoami_get_handler(httpd_req_t *req);
static esp_err_t scan_get_handler(httpd_req_t *req);
static esp_err_t default_captive_handler(httpd_req_t *req, httpd_err_code_t err);

/* HTML page served by GET /. Title is the device name from the
 * orchestrator's provisioning_app_info_t (CONFIG defaults are
 * fine on a fresh bring-up). Hand-rolled to keep cJSON out
 * of the hot path for what is fundamentally a static page.
 *
 * The page is intentionally lean (currently ~6.1 KB after the
 * password show/hide toggle, the centered title restructure,
 * and the server-side wifi bars landed). It is compiled into
 * the firmware's .rodata so every byte counts. JavaScript is
 * limited to the network dropdown (scan, dedup, sort, submit
 * guard) and the password show/hide toggle — the form itself is
 * a native POST so the /provision handler can keep its existing
 * form-urlencoded contract unchanged. */
static const char *HTML_FORM_BODY =
"<!DOCTYPE html><html lang='en'><head>"
"<meta charset='utf-8'>"
"<meta name='viewport' content='width=device-width,initial-scale=1'>"
"<meta name='theme-color' content='#0b1220'>"
"<title>Witsaba Cam Setup</title>"
"<style>"
":root{--b:#f5f7fb;--f:#0b1220;--m:#5b6478;--l:#d8dce5;--p:#1f6feb;--w:#fff;--o:#16a34a;--e:#dc2626}"
"@media(prefers-color-scheme:dark){:root{--b:#0b1220;--f:#e6e9ef;--m:#9aa3b2;--l:#2a3344;--p:#58a6ff;--w:#0b1220}.er{background:#3a1414;border-color:#5a2222}.ok{background:#0f2a17;border-color:#1c4426}}"
"@media(prefers-reduced-motion:reduce){*,::before,::after{animation-duration:.01ms!important;transition-duration:.01ms!important}}"
"*{box-sizing:border-box}"
"html,body{margin:0;background:var(--b);color:var(--f);font:16px/1.45 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif}"
"main{max-width:32rem;margin:0 auto;padding:1.25rem 1rem 2rem}"
"header{text-align:center;margin:0 0 1.5rem;padding-bottom:1.25rem;border-bottom:1px solid var(--l)}"
"h1{font-size:1.5rem;margin:0;font-weight:700;color:var(--p)}"
"h2{font-size:.95rem;margin:.35rem 0 0;font-weight:500;color:var(--m)}"
".bg{font-size:.7rem;color:var(--m);border:1px solid var(--l);border-radius:999px;padding:.15rem .55rem;display:inline-block;margin-top:.6rem}"
"p.l{margin:0 0 1.5rem;color:var(--m);font-size:.95rem;line-height:1.5}p.l b{color:var(--f)}"
"form{border:1px solid var(--l);border-radius:12px;padding:1.25rem}"
".r{margin-bottom:1rem}.r:last-of-type{margin-bottom:0}"
"label.lb{display:block;font-size:.8rem;font-weight:600;color:var(--m);margin-bottom:.3rem}"
".pw{display:flex;gap:.4rem}"
".pw input{flex:1}"
".pw button{flex:0 0 auto;padding:.7rem .85rem;font-size:.85rem;color:var(--m);background:transparent;border:1px solid var(--l);border-radius:8px;cursor:pointer}"
"select,input[type=text],input[type=password]{width:100%;padding:.7rem;font:inherit;color:var(--f);background:var(--b);border:1px solid var(--l);border-radius:8px}"
"select:focus,input:focus,button:focus{outline:2px solid var(--p);outline-offset:1px;border-color:var(--p)}"
"input:user-invalid{border-color:var(--e)}"
"details>summary{list-style:none;cursor:pointer;color:var(--p);font-size:.82rem;padding:.2rem 0}"
"details>summary::-webkit-details-marker,details>summary::marker{display:none}"
"details>summary::before{content:'+ ';font-weight:700}"
"details[open]>summary::before{content:'- '}"
"details input{margin-top:.4rem}"
".bn{display:flex;gap:.5rem;margin-top:1.25rem}"
"button{font:inherit;font-weight:600;padding:.85rem 1rem;border-radius:8px;border:1px solid transparent;cursor:pointer}"
".pr{background:var(--p);color:var(--w);flex:2}.pr:disabled{opacity:.55;cursor:not-allowed}"
".se{background:transparent;color:var(--f);border-color:var(--l);flex:1}"
".ms{margin-top:1rem;padding:.7rem .9rem;border-radius:8px;font-size:.85rem;display:none}"
".er{background:#fde8e8;color:var(--e);border:1px solid #f5b5b5}"
".ok{background:#e8f7ec;color:var(--o);border:1px solid #b5e1bf}"
"</style></head><body><main>"
"<header>"
"<h1>Witsaba Cam Setup</h1>"
"<h2>{deviceName}</h2>"
"<span class='bg' aria-label='WPA2 encrypted'>WPA2</span>"
"</header>"
"<p class='l'>Pick your home network. <b>This device only supports 2.4 GHz</b> - if your router shows two SSIDs, choose the 2.4 GHz one.</p>"
"<form method='POST' action='/provision' novalidate>"
"<div class='r'>"
"<label class='lb' for='ssid'>Network</label>"
"<select id='ssid' name='ssid' required><option value=''>Scanning...</option></select>"
"<details><summary>Add hidden network manually</summary>"
"<input type='text' id='ssid_h' name='ssid_h' maxlength='32' placeholder='Network name'></details>"
"</div>"
"<div class='r'>"
"<label class='lb' for='password'>Password</label>"
"<div class='pw'>"
"<input type='password' id='password' name='password' minlength='8' maxlength='64' autocomplete='current-password' required>"
"<button type='button' id='pwshow' aria-label='Show password' aria-pressed='false'>Show</button>"
"</div>"
"</div>"
"<div class='bn'>"
"<button class='se' type='button' id='rescan'>Rescan</button>"
"<button class='pr' type='submit'>Connect</button>"
"</div>"
"<div class='ms er' id='err' role='alert' aria-live='assertive'></div>"
"<div class='ms ok' id='ok' role='status' aria-live='polite'></div>"
"</form>"
"<script>"
"(function(){"
"var s=document.getElementById('ssid'),h=document.getElementById('ssid_h'),p=document.getElementById('password'),b=document.getElementById('rescan'),e=document.getElementById('err'),sub=document.querySelector('.pr'),pw=document.getElementById('pwshow');"
"function E(m){e.textContent=m;e.style.display='block'}"
"function scan(){s.disabled=true;b.disabled=true;fetch('/scan',{cache:'no-store'}).then(function(r){return r.json()}).then(function(l){"
"var seen={};(l||[]).forEach(function(n){if(!seen[n.ssid]||seen[n.ssid].rssi<n.rssi)seen[n.ssid]=n;});"
"var sr=Object.keys(seen).map(function(k){return seen[k]}).sort(function(a,b){return b.rssi-a.rssi});"
"s.innerHTML='';if(!sr.length){var o=document.createElement('option');o.value='';o.textContent='No 2.4 GHz networks found';s.appendChild(o);}else{"
"sr.forEach(function(n){var o=document.createElement('option');o.value=n.ssid;o.textContent=n.ssid+' '+n.bars;s.appendChild(o);});"
"var o=document.createElement('option');o.value='_hid_';o.textContent='- Hidden (type above) -';s.appendChild(o);}"
"s.disabled=false;b.disabled=false;}).catch(function(){s.disabled=false;b.disabled=false;E('Scan failed - reload to retry');});}"
"s.addEventListener('change',function(){h.required=s.value==='_hid_';if(s.value==='_hid_')h.focus();});"
"pw.addEventListener('click',function(){var s=p.type==='text';p.type=s?'password':'text';pw.textContent=s?'Show':'Hide';pw.setAttribute('aria-pressed',s?'false':'true');p.focus();});"
"document.querySelector('form').addEventListener('submit',function(ev){"
"var ssid=s.value==='_hid_'?h.value.trim():s.value;"
"if(!ssid){ev.preventDefault();E('Choose a network or add a hidden one');return;}"
"if(ssid.length>32){ev.preventDefault();E('Network name too long');return;}"
"if(p.value.length<8){ev.preventDefault();E('Password must be at least 8 characters');return;}"
"if(s.value==='_hid_'){var opt=s.querySelector('option[value=\"_hid_\"]');if(opt){opt.value=ssid;opt.textContent=ssid+' (manual)';s.value=ssid;}}"
"sub.disabled=true;sub.textContent='Connecting...';});"
"b.addEventListener('click',scan);scan();"
"})();"
"</script>"
"</main></body></html>";

/* Substitute {deviceName} occurrences in the HTML body with the
 * CONFIG-defined device name. The placeholder is 12 bytes; the
 * device name is a Kconfig string whose length is bounded at
 * build time (PROV_NAME_MAX_LEN), so the output buffer is the
 * input size plus a small headroom. Returns the final byte
 * length written to `out` (excluding NUL). */
static size_t html_substitute_device_name(const char *in, size_t in_len,
                                          char *out, size_t out_size)
{
    const char *name = CONFIG_PROVISIONING_DEVICE_NAME;
    size_t name_len = strlen(name);
    size_t o = 0;
    for (size_t i = 0; i < in_len; ++i) {
        if (i + 12 <= in_len &&
            memcmp(in + i, "{deviceName}", 12) == 0 &&
            o + name_len + 1 < out_size) {
            memcpy(out + o, name, name_len);
            o += name_len;
            i += 11; /* +1 from the loop */
        } else if (o + 1 < out_size) {
            out[o++] = in[i];
        }
    }
    out[o] = '\0';
    return o;
}

/* GET / — return the HTML form. */
static esp_err_t root_get_handler(httpd_req_t *req)
{
    httpd_resp_set_type(req, "text/html; charset=utf-8");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");
    size_t body_len = strlen(HTML_FORM_BODY);
    /* Worst case: body + 32-byte device name per replacement.
     * The page has two {deviceName} occurrences, so 64 bytes
     * of headroom is comfortable. */
    size_t buf_size = body_len + 64;
    char *buf = (char *)malloc(buf_size);
    if (!buf) {
        /* OOM: serve the body without substitution. */
        return httpd_resp_send(req, HTML_FORM_BODY, body_len);
    }
    size_t out_len = html_substitute_device_name(
        HTML_FORM_BODY, body_len, buf, buf_size);
    esp_err_t r = httpd_resp_send(req, buf, out_len);
    free(buf);
    return r;
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

#if 0 /* CAPTIVE_VERBOSE_LOG: receipt trace for the operator submit.
        Flip to 1 in sdkconfig.defaults when you need the trace. */
    ESP_LOGI(TAG, "POST /provision: ssid=%s password=(redacted, len=%d)",
             fd.ssid, (int)strlen(fd.password));
#endif

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

/* The WiFi scan task. Runs on a proper FreeRTOS task (not
 * the HTTPD worker thread), so esp_wifi_scan_start() has the
 * correct pthread context and does not crash with LoadProhibited.
 * Writes results to s_scan_cache and signals s_scan_done_sema
 * before deleting itself. */
static void wifi_scan_task(void *arg)
{
    SemaphoreHandle_t done = (SemaphoreHandle_t)arg;

    wifi_scan_config_t scan_cfg = {
        .show_hidden = false,
        .scan_type   = WIFI_SCAN_TYPE_ACTIVE,
    };

#if 0 /* CAPTIVE_VERBOSE_LOG: scan start trace. Flip 1 for debug. */
    ESP_LOGI(TAG, "wifi_scan_task: starting active scan (max %d APs)",
             MAX_SCAN_AP);
#endif

    esp_err_t r = esp_wifi_scan_start(&scan_cfg, true);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "wifi_scan_task: esp_wifi_scan_start: %s",
                 esp_err_to_name(r));
        goto done;
    }

    wifi_ap_record_t ap_info[MAX_SCAN_AP] = {0};
    uint16_t n = MAX_SCAN_AP;
    r = esp_wifi_scan_get_ap_records(&n, ap_info);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "wifi_scan_task: esp_wifi_scan_get_ap_records: %s",
                 esp_err_to_name(r));
        goto done;
    }

    int out_count = 0;
    char ssid_buf[MAX_SCAN_AP][33] = {{0}};
    int8_t rssi_buf[MAX_SCAN_AP] = {0};

    for (uint16_t i = 0; i < n && out_count < MAX_SCAN_AP; i++) {
        /* Skip hidden SSIDs — operator cannot select what they
         * cannot read. */
        if (ap_info[i].ssid[0] == '\0') continue;

        /* Skip enterprise / WPA3-enterprise: no supplicant available
         * on ESP32 station without extra software. */
        wifi_auth_mode_t m = ap_info[i].authmode;
        if (m == WIFI_AUTH_WPA2_ENTERPRISE ||
            m == WIFI_AUTH_WPA3_ENTERPRISE) {
            continue;
        }

        memcpy(ssid_buf[out_count], ap_info[i].ssid,
               sizeof(ap_info[i].ssid));
        rssi_buf[out_count] = ap_info[i].rssi;
        out_count++;
    }

    /* Write results to the shared cache (written here, read by the
     * HTTPD handler after the sema is given). */
    memset(&s_scan_cache, 0, sizeof(s_scan_cache));
    s_scan_cache.populated = true;
    s_scan_cache.count = out_count;
    for (int i = 0; i < out_count; i++) {
        memcpy(s_scan_cache.ssid[i], ssid_buf[i], 33);
        s_scan_cache.rssi[i] = rssi_buf[i];
    }

#if 0 /* CAPTIVE_VERBOSE_LOG: scan result count. Flip 1 for debug. */
    ESP_LOGI(TAG, "wifi_scan_task: found %d usable networks", out_count);
#endif

done:
    xSemaphoreGive(done);
    vTaskDelete(NULL);
}

/* GET /scan — spawns the WiFi scan task and blocks until results
 * are ready. Returns a JSON array: [{"ssid":"...","rssi":...},...]
 *
 * CRITICAL: esp_wifi_scan_start() MUST run on a FreeRTOS task that
 * owns the WiFi pthread TLS key. Calling it from the HTTPD worker
 * thread causes LoadProhibited in pthread_getspecific() (core dump
 * from real device, confirmed). The task stack is static (4 KB,
 * no heap allocation). */

/* WiFi signal-strength meter — four UTF-8 block characters where
 * U+2588 (█) is "filled" and U+2591 (░) is "empty". The thresholds
 * (-50 / -67 / -75 / -85 dBm) are the conventional values used by
 * most OS-level signal meters and map roughly to "excellent / good
 * / usable / weak / no signal". Returned literals are 12 bytes
 * each (4 chars × 3 bytes UTF-8) and live in .rodata.
 *
 * Operators do not read dBm at provisioning time; the meter is
 * enough context for "is this my AP, and is it close enough to
 * the softAP to talk back?" */
static const char *get_wifi_bars_meter(int8_t rssi)
{
    if (rssi >= -50) {
        return "\xE2\x96\x88\xE2\x96\x88\xE2\x96\x88\xE2\x96\x88"; /* ████ */
    } else if (rssi >= -67) {
        return "\xE2\x96\x88\xE2\x96\x88\xE2\x96\x88\xE2\x96\x91"; /* ███░ */
    } else if (rssi >= -75) {
        return "\xE2\x96\x88\xE2\x96\x88\xE2\x96\x91\xE2\x96\x91"; /* ██░░ */
    } else if (rssi >= -85) {
        return "\xE2\x96\x88\xE2\x96\x91\xE2\x96\x91\xE2\x96\x91"; /* █░░░ */
    } else {
        return "\xE2\x96\x91\xE2\x96\x91\xE2\x96\x91\xE2\x96\x91"; /* ░░░░ */
    }
}

static esp_err_t scan_get_handler(httpd_req_t *req)
{
    httpd_resp_set_type(req, "application/json");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");

    if (!s_scan_done_sema) {
        httpd_resp_sendstr(req, "[]");
        return ESP_OK;
    }

    /* Take the semaphore — blocks if a scan is already running
     * (only one scan at a time to keep the cache consistent).
     * portMAX_DELAY is safe here because the HTTPD worker can
     * block without affecting other connections. */
    if (xSemaphoreTake(s_scan_done_sema, portMAX_DELAY) != pdTRUE) {
        httpd_resp_sendstr(req, "[]");
        return ESP_OK;
    }

    /* Give the sema back so the task can re-give it when done.
     * The task will do xSemaphoreGive(done) as its last action. */
    xSemaphoreGive(s_scan_done_sema);

    /* xTaskCreate (heap-allocated) is used instead of xTaskCreateStatic
     * because ESP-IDF v5.5.3's xTaskCreateStatic requires the TCB
     * buffer to satisfy strict alignment constraints that a plain
     * .bss static variable does not guarantee — confirmed by
     * StoreProhibited panic in prvAddNewTaskToReadyList when the
     * static buffers happened to be misaligned. The ESP32 heap has
     * ~147 KB free; a ~10 KB scan task is trivial overhead. */
    TaskHandle_t task_h = NULL;
    BaseType_t r = xTaskCreate(
        wifi_scan_task,          /* entry */
        "wifi_scan",             /* name */
        8192,                    /* stack size in bytes */
        s_scan_done_sema,        /* args = done sema */
        3,                       /* priority (below HTTPD) */
        &task_h                  /* output handle */
    );

    if (r != pdPASS || task_h == NULL) {
        ESP_LOGE(TAG, "/scan: xTaskCreate failed (heap full?)");
        httpd_resp_sendstr(req, "[]");
        return ESP_OK;
    }

    /* Block until the scan task populates the cache and gives the
     * sema. While blocked the HTTPD worker is paused but the softAP
     * and any other HTTPD handlers on different worker threads
     * keep running. */
    xSemaphoreTake(s_scan_done_sema, portMAX_DELAY);

    /* Cache is now populated (or empty on error). Build the JSON
     * response from the cache. */
    char resp[2048] = "[";
    size_t off = 1;
    for (int i = 0; i < s_scan_cache.count && off < sizeof(resp) - 4; i++) {
        if (i > 0) resp[off++] = ',';
        /* Bars are emitted as-is from .rodata (UTF-8). rssi is
         * kept in the payload even though the operator UI no
         * longer renders it; callers that want to do their own
         * meter (e.g. a future home-assistant pairing screen)
         * still get the raw value. */
        int n = snprintf(resp + off, sizeof(resp) - off,
                         "{\"ssid\":\"%s\",\"rssi\":%d,\"bars\":\"%s\"}",
                         s_scan_cache.ssid[i], s_scan_cache.rssi[i],
                         get_wifi_bars_meter(s_scan_cache.rssi[i]));
        if (n < 0 || (size_t)n >= sizeof(resp) - off) break;
        off += n;
    }
    resp[off++] = ']';
    resp[off] = '\0';

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
        /* Same body as root_get_handler — naive captive-portal probes
         * should still see the form, including the device-name
         * substitution. */
        httpd_resp_set_type(req, "text/html; charset=utf-8");
        httpd_resp_set_hdr(req, "Cache-Control", "no-store");
        size_t body_len = strlen(HTML_FORM_BODY);
        size_t buf_size = body_len + 64;
        char *buf = (char *)malloc(buf_size);
        if (!buf) {
            return httpd_resp_send(req, HTML_FORM_BODY, body_len);
        }
        size_t out_len = html_substitute_device_name(
            HTML_FORM_BODY, body_len, buf, buf_size);
        esp_err_t r = httpd_resp_send(req, buf, out_len);
        free(buf);
        return r;
    }
    httpd_resp_set_status(req, "404 Not Found");
    httpd_resp_sendstr(req, "Not Found");
    return ESP_OK;
}

esp_err_t captive_portal_bring_up(void)
{
    if (s_captive_httpd) {
#if 0 /* CAPTIVE_VERBOSE_LOG: idempotent re-bring-up. Flip 1 for debug. */
        ESP_LOGW(TAG, "bring_up: already running");
#endif
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
#if 0 /* CAPTIVE_VERBOSE_LOG: bring-up confirmation. Flip 1 for debug. */
    ESP_LOGI(TAG, "captive httpd running on port %d",
             httpd_cfg.server_port);
#endif

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

    /* Binary semaphore for scan task synchronisation. Static
     * allocation — no heap. Starts ''taken'' so the handler
     * blocks correctly on the first xSemaphoreTake. */
    s_scan_done_sema = xSemaphoreCreateBinaryStatic(&s_scan_done_sema_buf);
    if (!s_scan_done_sema) {
        ESP_LOGE(TAG, "bring_up: xSemaphoreCreateBinaryStatic failed");
        httpd_stop(s_captive_httpd);
        s_captive_httpd = NULL;
        return ESP_ERR_NO_MEM;
    }
    /* Give it so the handler's first take() succeeds and the
     * task's give() unblocks it. */
    xSemaphoreGive(s_scan_done_sema);

    return ESP_OK;
}

void captive_portal_tear_down(void)
{
    if (!s_captive_httpd) {
        return;
    }
    /* unregister_default isn't an IDF function; httpd_stop on
     * the handle cleans up. */
    /* Invalidate the scan semaphore — any in-flight GET /scan
     * will return [] when it finds s_scan_done_sema == NULL.
     * The scan task holds its own copy of the handle so it is
     * not affected by this write. */
    s_scan_done_sema = NULL;

    esp_err_t r = httpd_stop(s_captive_httpd);
    if (r != ESP_OK) {
#if 0 /* CAPTIVE_VERBOSE_LOG: tear_down stop warning. Flip 1 for debug. */
        ESP_LOGW(TAG, "tear_down: httpd_stop: %s", esp_err_to_name(r));
#endif
    }
    s_captive_httpd = NULL;
}
