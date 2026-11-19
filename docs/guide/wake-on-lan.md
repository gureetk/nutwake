# Set up Wake-on-LAN

Wake-on-LAN is the part that most often needs work, and it is set up on the nodes, not in nutwake. Do this for each node.

**In the BIOS or UEFI,** turn on Wake-on-LAN (it may be called "Power on by PCI-E" or similar). Set "Restore on AC power loss" to "Last state" or "Power off", **not** "Power on". With "Power on" the node starts the moment the UPS has power again, which skips the wait for the battery.

**On the network card,** Wake-on-LAN must be set to `g`:

```sh
ethtool nic0 | grep Wake-on      # "Wake-on: g" is what you want
sudo ethtool -s nic0 wol g       # turns it on until the next reboot
ethtool -P nic0                  # the permanent MAC address: this is the `mac` for the node
```

To keep it across reboots on Proxmox (or any machine using `/etc/network/interfaces`), add a line under the card's stanza:

```
iface nic0 inet manual
    post-up /usr/sbin/ethtool -s nic0 wol g
```

Use the MAC of the physical card, not of a bridge such as `vmbr0`.

**On the network,** the Pi's wake packet is a broadcast, so the Pi must be on the same network segment (the same VLAN) as the nodes. Set `wake.broadcast` to your subnet's broadcast address, for example `192.168.1.255:9`. The default, `255.255.255.255:9`, is dropped by some switches and routers. If the Pi and the nodes are on different VLANs, give the Pi an interface in the nodes' VLAN, or the packet will not arrive. Check that the Pi can ping each node.

To test, shut a node down by hand and run `nutwake wake <name>` from the Pi.
