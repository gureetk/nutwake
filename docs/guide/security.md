# Security

- **The status page has no login.** Anyone who can reach it can see when you are out and whether your nodes are up. Keep it on your LAN. If you listen on more than loopback, limit who can reach the port with the firewall, for example by adding a rule like this to your input chain: `tcp dport 8080 ip saddr 192.168.1.0/24 accept`, followed by a rule that drops the rest.
- **The page is read-only on purpose.** The shutdown command runs as root, so a page that could change settings would be a way to run commands on the Pi. Settings are shown on the page, but changed in the file.
- **The daemon runs as the `nutwake` user** with only the permission to ping, and cannot write outside `/var/lib/nutwake`. The shutdown helper runs as root because upsmon runs it.
- **The helper does not trust a configuration that others can change.** Running as root, if the configuration file or its directory belongs to someone other than root or can be written by anyone else, the helper ignores `shutdown.command` and halts with `/sbin/shutdown -h +0`. `nutwake check` warns about it.
