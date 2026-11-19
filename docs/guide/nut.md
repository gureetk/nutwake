# Set up NUT

nutwake reads the UPS from NUT's server (`upsd`). It needs no password for that. This is the usual layout: the Pi is the NUT primary, and each node runs its own NUT client that shuts the node down when the Pi says so.

On the Pi (the lines that matter):

```
# /etc/nut/nut.conf
MODE=netserver

# /etc/nut/ups.conf
[ups]
    driver = usbhid-ups
    port = auto

# /etc/nut/upsd.conf: listen for nutwake on loopback and for the nodes on the LAN
LISTEN 127.0.0.1 3493
LISTEN 192.168.1.5 3493

# /etc/nut/upsd.users
[upsmon]
    password = choose-one
    upsmon primary

# /etc/nut/upsmon.conf
MONITOR ups@localhost 1 upsmon choose-one primary
SHUTDOWNCMD "/usr/local/bin/nutwake shutdown -config /etc/nutwake/config.yaml"
```

On each node:

```
# /etc/nut/nut.conf
MODE=netclient

# /etc/nut/upsmon.conf
MONITOR ups@192.168.1.5 1 upsmon choose-one secondary
```

NUT before version 2.8 spells `primary` as `master` and `secondary` as `slave`. The name in `[ups]` must match `nut.ups` in nutwake's configuration.
