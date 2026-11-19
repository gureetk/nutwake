# Commands

| Command | What it does |
| --- | --- |
| `nutwake serve` | Runs the daemon: watches the UPS and the nodes, wakes nodes when power is back, and serves the status page. |
| `nutwake shutdown` | The helper for upsmon's `SHUTDOWNCMD`. Waits for the nodes to power off, then halts this machine. |
| `nutwake shutdown --dry-run` | Shows what the helper would do, and changes nothing. |
| `nutwake wake <node>...` | Sends a wake packet now. `-all` wakes every node. |
| `nutwake check` | Tests the configuration, the UPS, the nodes, the halt command and the wake settings. Exits 1 if something fails. |
| `nutwake demo` | Serves the status page with made-up data. `-scene online`, `battery` or `waiting` picks the moment; `-addr` picks the address. |
| `nutwake version` | Prints the version. |

Every command takes `-config <file>`, which defaults to `/etc/nutwake/config.yaml` or `$NUTWAKE_CONFIG`. To change a setting, edit the file and run `sudo systemctl restart nutwake`.
