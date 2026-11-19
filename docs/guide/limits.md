# Limits

Things to know before you rely on it.

- **`shutdown.wait_for_nodes` has to fit in what the UPS has left.** The helper starts when the UPS signals a low battery, and the battery runs out some minutes later. If the wait is longer than that, the Pi may lose power before it halts. The default is 5 minutes. Look at how long your UPS really lasts at that point, and keep the wait shorter.
- **Prefer a `tcp:<port>` probe, or have `ping` installed.** The `icmp` probe runs the `ping` command (`iputils-ping`). `nutwake check` says if it is missing.
- **nutwake halts the Pi but does not switch the UPS off.** That is NUT's job, through `upsmon` and the UPS driver's shutdown. Either way, nodes set to stay off after a power loss are woken by nutwake when it is time.
- **One UPS and one Pi.** There is no clustering and no second watcher. If the Pi is off when the power comes back, nothing wakes the nodes.
- **No alerts.** It records and shows. It does not send email or messages.
- **Configuration is read at start.** Restart the service after a change.
