# Security Policy

## Enterprise Vulnerability Disclosure & Management

The Portage Platform (`Portage.sh`, `Synapse-CDC`, `Ghostwire`) is designed for mission-critical, air-gapped, and zero-trust enterprise environments. We treat the confidentiality, integrity, and availability of stateful migration systems with the highest priority.

---

## Supported Versions

Only the latest active major/minor release tracks receive security updates and CVE patches:

| Version | Supported | Security Maintenance Status |
| :--- | :--- | :--- |
| 1.0.x | Yes | Full Active Support |
| < 1.0 | No | Unsupported |

---

## Reporting a Vulnerability

**Please do NOT report security vulnerabilities through public GitHub issues, discussions, or pull requests.**

### Private Disclosure Channels

To report a vulnerability responsibly:

1. **GitHub Private Security Advisory (Preferred):**
   Navigate to the repository's **Security** tab and click **Report a vulnerability** to open a confidential discussion draft.
2. **Direct Security Contact:**
   Email the repository owner directly at:
   - **Primary Maintainer:** [kunalkhare2004@gmail.com](mailto:kunalkhare2004@gmail.com)
   - **Subject Prefix:** `[SECURITY DISCLOSURE] Portage Platform - <Brief Description>`

### Information to Include in Your Report

To help us triage and patch the issue promptly, please include:
- A descriptive summary of the vulnerability (e.g. Memory Safety, Remote Code Execution, Privilege Escalation, AST Injection, Replay Attack).
- Detailed steps to reproduce the issue, including proof-of-concept (PoC) scripts or minimal manifests.
- Affected component(s) (`portage-operator`, `synapse-cdc`, `ghostwire-proxy`).
- The environmental conditions (e.g., Kubernetes version, CSI storage provider, air-gapped constraints).
- Any potential impact on confidentiality, integrity, or zero-downtime guarantees.

---

## Response & Remediation SLA

Our security response team commits to the following enterprise service level agreements (SLAs):

| Milestone | Target SLA | Description |
| :--- | :--- | :--- |
| **Acknowledgment** | Within **24 Hours** | Confirmation of report receipt and initial assessment. |
| **Triage & Reproduction** | Within **72 Hours** | Verification of impact, severity rating (CVSS v3.1), and scope. |
| **Remediation & Patch** | Within **7 Days** | Development, testing in air-gapped staging, and private review. |
| **Public Advisory (CVE)** | Post Patch (30-day window) | Coordinated disclosure with CVE assignment. |

---

## Security Architectural Guarantees

1. **Air-Gapped Strict Isolation:**
   - Zero outbound internet telemetry or runtime phone-home connections.
   - Self-contained binaries with no external CDN or registry dependencies at runtime.
2. **Mathematical Verifiability:**
   - Cryptographic SHA-256 Merkle-tree state verification ensures zero bit-rot or undetected data divergence between source and target clusters.
3. **Least Privilege & Role-Based Access Control:**
   - Portage operator RBAC is strictly scoped to designated migration namespaces.
   - CSI snapshotting requires storage-specific RBAC roles and prevents privilege escalation.
4. **Memory Safety & Determinism:**
   - Data plane engines (`Synapse-CDC` and `Ghostwire`) are implemented in safe Rust, eliminating entire classes of vulnerabilities such as use-after-free, buffer overflows, and race conditions.

---

## Safe Harbor

We consider security research conducted under this policy to be authorized. We will not pursue legal action against researchers who:
- Act in good faith to avoid privacy violations, data destruction, and service interruption.
- Keep vulnerability details strictly confidential until authorized release.
- Do not exploit identified vulnerabilities beyond what is minimally required to demonstrate a PoC.
