# Ion on systemd (bare metal)

Run Ion as a hardened systemd service on a Linux host.

## Install

```bash
# 1. Install the binary
sudo install -m 0755 bin/ion /usr/local/bin/ion

# 2. Create a dedicated system user and state directory
sudo useradd --system --home-dir /var/lib/ion --shell /usr/sbin/nologin ion
sudo install -d -o ion -g ion -m 0700 /var/lib/ion

# 3. Optional environment file
sudo install -d -m 0755 /etc/ion
sudo install -m 0600 deploy/systemd/environment.example /etc/ion/environment

# 4. Initialize the vault (as the service user, against a host key source)
sudo -u ion /usr/local/bin/ion init --data-dir /var/lib/ion

# 5. Install and start the unit
sudo install -m 0644 deploy/systemd/ion.service /etc/systemd/system/ion.service
sudo systemctl daemon-reload
sudo systemctl enable --now ion.service
```

## Status and logs

```bash
systemctl status ion.service
journalctl -u ion.service -f
```

## Remote access

The unit binds plain HTTP to `127.0.0.1:4174`. To serve Ion beyond the host,
terminate TLS in a reverse proxy (nginx, Caddy, or similar) on the same host and
proxy to `127.0.0.1:4174`. Do not change the listen address to a non-loopback
address for plain HTTP — Ion rejects it.

## Hardening

The unit applies a strict sandbox (`ProtectSystem=strict`, `NoNewPrivileges`,
`MemoryDenyWriteExecute`, restricted address families and namespaces, and a
writable path limited to the state directory). Review these against your host
policy before enabling.
