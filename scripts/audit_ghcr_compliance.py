#!/usr/bin/env python3
"""
Portage Enterprise - GHCR Compliance & Security Audit Script
Audits GitHub Container Registry (GHCR) package visibility, token permissions,
and package integrity for kunaldevxxx enterprise distributions.
"""

import json
import os
import sys
import urllib.request
import urllib.error
from datetime import datetime


def gh_api_request(url, token):
    """Make authenticated request to GitHub API using standard library."""
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "User-Agent": "Portage-Enterprise-Audit",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urllib.request.urlopen(req) as resp:
            headers = dict(resp.headers)
            body = resp.read().decode("utf-8")
            data = json.loads(body) if body else {}
            return data, headers, resp.status
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8")
        return {"error": str(e), "details": err_body}, dict(e.headers), e.code
    except Exception as e:
        return {"error": str(e)}, {}, 500


def main():
    token = os.environ.get("GITHUB_TOKEN", "").strip()
    if not token:
        print("ERROR: GITHUB_TOKEN environment variable is required.", file=sys.stderr)
        sys.exit(1)

    print("=" * 64)
    print(" Portage Enterprise: GHCR Security & Compliance Audit")
    print(f" Timestamp: {datetime.utcnow().isoformat()}Z")
    print("=" * 64)

    alerts = []

    # 1. Verify authenticated user & scopes
    user_data, user_headers, user_status = gh_api_request("https://api.github.com/user", token)
    if user_status != 200:
        print(f"✗ Failed to authenticate with provided token (HTTP {user_status}): {user_data.get('error')}")
        sys.exit(1)

    username = user_data.get("login", "unknown")
    oauth_scopes = user_headers.get("x-oauth-scopes", "none")
    print(f"\n[1] Authenticated User: {username}")
    print(f"    Token Scopes: {oauth_scopes}")

    if "read:packages" not in oauth_scopes and "write:packages" not in oauth_scopes and "repo" not in oauth_scopes:
        alerts.append("ALERT: Token lacks 'read:packages' or 'write:packages' scope.")

    # 2. Check user container packages
    packages_data, _, pkg_status = gh_api_request(
        "https://api.github.com/user/packages?package_type=container", token
    )

    if pkg_status != 200:
        # Fallback to public/user endpoint
        packages_data, _, pkg_status = gh_api_request(
            f"https://api.github.com/users/{username}/packages?package_type=container", token
        )

    print("\n[2] GHCR Container Packages:")
    target_enterprise_pkgs = ["portage-enterprise-synapse-cdc", "portage-enterprise-ghostwire"]
    found_pkgs = {}

    if isinstance(packages_data, list):
        print(f"    Total container packages discovered: {len(packages_data)}")
        for pkg in packages_data:
            pkg_name = pkg.get("name", "")
            visibility = pkg.get("visibility", "unknown")
            created = pkg.get("created_at", "")
            updated = pkg.get("updated_at", "")
            found_pkgs[pkg_name] = pkg

            print(f"    - Package: {pkg_name} | Visibility: {visibility.upper()} | Updated: {updated}")

            # Verify enterprise packages must remain private
            for ent_pkg in target_enterprise_pkgs:
                if ent_pkg in pkg_name and visibility.lower() != "private":
                    alert_msg = f"ALERT: Enterprise package '{pkg_name}' is {visibility.upper()}! Must be PRIVATE."
                    alerts.append(alert_msg)
    else:
        print(f"    Notice: No packages returned or API message: {packages_data.get('message', 'None')}")

    # 3. Check for specific enterprise images
    print("\n[3] Enterprise Image Verification:")
    for ent_pkg in target_enterprise_pkgs:
        matching = [p for p in found_pkgs.keys() if ent_pkg in p]
        if matching:
            pkg_info = found_pkgs[matching[0]]
            status = "✓ SECURE (PRIVATE)" if pkg_info.get("visibility") == "private" else "⚠ NON-PRIVATE"
            print(f"    {status}: {matching[0]}")
        else:
            print(f"    ℹ Pending publish: {ent_pkg} (not yet pushed to GHCR)")

    # 4. Summary & Alerts
    print("\n" + "=" * 64)
    print(" Audit Summary")
    print("=" * 64)

    if alerts:
        print(f"⚠️  {len(alerts)} ALERTS DETECTED:")
        for alert in alerts:
            print(f"   {alert}")
    else:
        print("✓ All audited security and compliance checks PASSED.")

    # 5. Output to GitHub Step Summary if running in GitHub Actions
    step_summary_file = os.environ.get("GITHUB_STEP_SUMMARY")
    if step_summary_file:
        try:
            with open(step_summary_file, "a", encoding="utf-8") as f:
                f.write("### 🛡️ Portage Enterprise GHCR Audit Summary\n\n")
                f.write(f"- **Audited User:** `{username}`\n")
                f.write(f"- **Token Scopes:** `{oauth_scopes}`\n")
                f.write(f"- **Audit Time:** `{datetime.utcnow().isoformat()}Z`\n\n")
                if alerts:
                    f.write("#### ⚠️ Security Alerts\n")
                    for alert in alerts:
                        f.write(f"- {alert}\n")
                else:
                    f.write("✅ **All GHCR compliance checks passed cleanly.**\n")
        except Exception as e:
            print(f"Notice: Failed to write to step summary: {e}", file=sys.stderr)

    if alerts:
        sys.exit(1)


if __name__ == "__main__":
    main()
