# Install

You need a Pi (or any Linux machine) that can reach the UPS through NUT, and the nodes on the same network. The steps below assume Raspberry Pi OS or Debian.

**nutwake is a pre-release.** It has been tested with a fake UPS and fake nodes, and has not yet been run against a real UPS and real nodes. Steps 4 and 7 are how you find out whether it works on yours.

**1. Get the binary.** Download `nutwake-linux-arm64` (a 64-bit system) or `nutwake-linux-armv7` (a 32-bit one), and `SHA256SUMS`, from the [latest release](https://github.com/gureetk/nutwake/releases). The steps below also use files from the repository (`contrib/` and `config.example.yaml`), so clone it, put the downloads in it, and run every command from inside it:

```sh
git clone https://github.com/gureetk/nutwake && cd nutwake
sha256sum -c --ignore-missing SHA256SUMS
uname -m          # aarch64 means a 64-bit system, armv7l a 32-bit one
sudo install -m 0755 nutwake-linux-arm64 /usr/local/bin/nutwake   # or nutwake-linux-armv7
nutwake version
```

To build it yourself instead, install Go and run `make cross` in the repository. The binaries are in `dist/`.

**2. Create the user the daemon runs as.**

```sh
sudo install -m 0644 contrib/nutwake.sysusers /usr/lib/sysusers.d/nutwake.conf
sudo systemd-sysusers
sudo install -d -m 0755 -o nutwake -g nutwake /var/lib/nutwake
```

**3. Write the configuration.** Only `nodes` is required.

```sh
sudo install -d -m 0755 -o root -g root /etc/nutwake
sudo install -m 0644 -o root -g root config.example.yaml /etc/nutwake/config.yaml
sudoedit /etc/nutwake/config.yaml
```

Every setting is explained in [`config.example.yaml`](../../config.example.yaml). Keep the file, and the directory it is in, owned by root and writable by nobody else. See [Security](security.md) for why.

**4. Check it.** This tests the configuration, reads the UPS, pings each node and finds the halt command, and says what is wrong in plain words:

```sh
sudo -u nutwake nutwake check
```

**5. Start the daemon.**

```sh
sudo install -m 0644 contrib/nutwake.service /etc/systemd/system/nutwake.service
sudo systemctl daemon-reload
sudo systemctl enable --now nutwake
```

The status page is at the address in `listen` (`127.0.0.1:8080` by default, which only the Pi itself can reach; use the Pi's own address or `0.0.0.0:8080` to reach it from elsewhere, and read [Security](security.md) first).

**6. Hand the shutdown to nutwake.** Put the `SHUTDOWNCMD` line from [`contrib/upsmon.conf.example`](../../contrib/upsmon.conf.example) in `/etc/nut/upsmon.conf` and restart upsmon (`sudo systemctl restart nut-monitor`). Then see what it would do, without doing it:

```sh
sudo nutwake shutdown --dry-run
```

**7. Pull the plug.** Nothing replaces this. With the nodes running, unplug the UPS from the wall, watch the status page, and plug it back in once the nodes are off and the Pi has halted. Do it once before you rely on it.
