#!/usr/bin/env python3
"""One-shot audit for the captive-portal redesign.

Reports:
  - HTML_FORM_BODY byte length (concatenation of all C string literals
    between the LHS = and the closing `";`).
  - captive_portal.c total file size.
  - ESP_LOG* call count (active vs gated under #if 0 / CAPTIVE_VERBOSE_LOG).
  - <label for=...> association count in HTML_FORM_BODY.
  - aria-live / role=alert / role=status region count in HTML_FORM_BODY.

No external deps; reads the C source and prints a table. Exit 0 always.
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(
    ROOT,
    "embedded/iot_cams/components/provisioning/src/captive_portal.c",
)


def measure_html_body(src: str) -> int:
    in_block = False
    body_len = 0
    for line in src.splitlines():
        if line.startswith("static const char *HTML_FORM_BODY ="):
            in_block = True
            continue
        if in_block:
            stripped = line.strip()
            if stripped == '";':
                break
            # Each literal is wrapped in "...", optionally with surrounding
            # whitespace. Strip exactly one leading and one trailing quote.
            if len(stripped) >= 2 and stripped[0] == '"' and stripped[-1] == '"':
                body_len += len(stripped) - 2
    return body_len


def extract_html_body(src: str) -> str:
    in_block = False
    parts = []
    for line in src.splitlines():
        if line.startswith("static const char *HTML_FORM_BODY ="):
            in_block = True
            continue
        if in_block:
            stripped = line.strip()
            if stripped == '";':
                break
            if len(stripped) >= 2 and stripped[0] == '"' and stripped[-1] == '"':
                parts.append(stripped[1:-1])
    return "".join(parts)


def count_pattern(src: str, pattern: str) -> int:
    return len(re.findall(pattern, src))


def main() -> int:
    if not os.path.isfile(SRC):
        print(f"!! captive_portal.c not found at {SRC}", file=sys.stderr)
        return 0
    with open(SRC, "r", encoding="utf-8") as f:
        src = f.read()

    html = extract_html_body(src)
    html_bytes = measure_html_body(src)

    # Quick sanity-check on the body extraction.
    if html_bytes == 0:
        print("!! HTML_FORM_BODY extraction returned 0 bytes; "
              "regex is broken", file=sys.stderr)

    total_log = count_pattern(src, r"\bESP_LOG[IEW]\b\(")
    gated_log = count_pattern(src, r"#if 0 /\* CAPTIVE_VERBOSE_LOG")
    active_log = total_log - gated_log  # each #if 0 block wraps exactly one ESP_LOG*

    label_for = count_pattern(html, r"<label\s+class='lb'\s+for='[^']+'>")
    aria_live = count_pattern(html, r"aria-live='(assertive|polite)'")
    role_alert = count_pattern(html, r"role='(alert|status)'")

    print(f"captive_portal.c file size     : {os.path.getsize(SRC):>6} bytes")
    print(f"  HTML_FORM_BODY size          : {html_bytes:>6} bytes")
    print(f"  ESP_LOG* total (inc. gated)  : {total_log:>6}")
    print(f"  ESP_LOG* gated (#if 0)       : {gated_log:>6}")
    print(f"  ESP_LOG* active (ungated)    : {active_log:>6}")
    print(f"  <label class='lb' for='..'>  : {label_for:>6}")
    print(f"  aria-live='..'               : {aria_live:>6}")
    print(f"  role='alert|status'          : {role_alert:>6}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
