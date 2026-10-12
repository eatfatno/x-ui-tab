# Third-party components

This repository contains an independent traffic monitor and deployment tools for
[MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui), initially tested with v3.9.0.
The project code is distributed under GPL-3.0; third-party files retain their
original copyright notices and license terms.

| Component | Source / corresponding source | License location |
| --- | --- | --- |
| Official 3x-ui v3.9.0, distributed unmodified in the Docker bundle | https://github.com/MHSanaei/3x-ui/tree/v3.9.0 | GPL-3.0; `licenses/3x-ui-LICENSE` in the bundle |
| Xray and Geo data distributed with the official Linux release | https://github.com/XTLS/Xray-core and the versions shipped by upstream | Original upstream licenses and notices apply |
| v2fly/domain-list-community domain rules | https://github.com/v2fly/domain-list-community | `apps/traffic-monitor/internal/monitor/rules/LICENSE.domain-list-community` |
| blackmatrix7/ios_rule_script app rules | https://github.com/blackmatrix7/ios_rule_script | `apps/traffic-monitor/internal/monitor/rules/sources/LICENSE.ios_rule_script.txt` |

Rule source versions, original URLs and checksums are recorded in
`apps/traffic-monitor/internal/monitor/rules/sources/manifest.json` and the rule
catalog. Go and JavaScript dependencies are recorded in `go.mod`, `go.sum`, and
the npm lockfiles. Their licenses apply independently.

The full upstream source archive quarantined by Windows Defender during local
development is not redistributed. The release documents the corresponding
upstream source location. No claim of a malware clearance is made.
