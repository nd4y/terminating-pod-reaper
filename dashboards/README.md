# Grafana dashboards

`terminating-pod-reaper.json` is the Grafana dashboard for this operator.
Grafana's dashboard sidecar isn't enabled on the instance this runs against,
so this file isn't auto-provisioned — it's the source of truth that's kept
in sync with Grafana by hand (or by whoever/whatever edits either side) via
the HTTP API.

Set `GRAFANA_URL` and `GRAFANA_TOKEN` (a service-account token) and
`GRAFANA_FOLDER_UID` (the folder this dashboard lives in) for your instance.
Dashboard uid: `aft2lg44ksc1sf`.

Pull the live dashboard into this file:

```bash
curl -s -H "Authorization: Bearer $GRAFANA_TOKEN" \
  "$GRAFANA_URL/api/dashboards/uid/aft2lg44ksc1sf" \
  | jq '.dashboard | del(.id, .version)' > dashboards/terminating-pod-reaper.json
```

Push this file to Grafana:

```bash
jq -n --slurpfile d dashboards/terminating-pod-reaper.json --arg folder "$GRAFANA_FOLDER_UID" \
  '{dashboard: $d[0], folderUid: $folder, overwrite: true}' \
  | curl -s -X POST -H "Authorization: Bearer $GRAFANA_TOKEN" -H "Content-Type: application/json" \
    --data-binary @- "$GRAFANA_URL/api/dashboards/db"
```

`id` and `version` are stripped/omitted on purpose — Grafana manages both and
they'd otherwise churn on every save.
