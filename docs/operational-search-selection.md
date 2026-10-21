# Operational search selection

Global palette operational results support these destinations:

| Result | Selection |
| --- | --- |
| Service workspace | Navigate to Service Workspaces only. Its existing open event restores terminal sessions and starts monitoring, so search must not activate it. |
| Monitor | Select the monitor and response-time chart; detailed check results remain hidden until requested. |
| Incident | Select the saved incident and its timeline; diagnostic/log evidence and timeline bodies stay collapsed. |
| Deployment | Select the saved project editor. Do not generate a plan or load pipeline runs. |
| Scan profile | Select the saved profile editor. Do not check tools or run a scan. |
| Security scan | Select that saved scan in scan history, with search/severity/comparison filters reset. Do not run a scan. |
| Runbook | Select the saved runbook editor. Do not preview, run, schedule, or select execution output. |
| Activity / audit | Navigate to Activity only. The current API exposes a bounded, filtered feed without a by-ID lookup or stable record-selection UI; a searched entry can have moved outside the feed. No direct-selection claim is made. |

Selection intents are in-memory `{ kind, id }` values only. They are consumed before fetching, including on failure or a missing record, and abandoned when navigation leaves the destination. Fresh records must match the requested ID; persisted envelope and body IDs must agree. Missing records show an error without falling back to another saved record. Responses from an older target, a departed panel, or a changed local selection cannot overwrite the current selection. Search does not persist record bodies or invoke operational execution methods.

The focused pure TypeScript tests cover routing, one-shot consumption, replacement, abandoned navigation, invalid identifiers, malformed records, and envelope/body ID mismatch. Desktop/Wails interactive testing requires the running application and a populated vault.
