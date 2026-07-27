# Configuration

Ion is configured through command-line flags and a small set of environment
variables. Flags take precedence over environment variables.

## Common flags

| Flag | Default | Applies to | Description |
|---|---|---|---|
| `--data-dir` | `~/.ion` | all | Durable data directory (SQLite, vault, work state). |
| `--listen` | `127.0.0.1:4174` | `dashboard` | Web operator listen address. Plain HTTP must bind loopback. |
| `--origin` | listen URL | `dashboard` | Exact browser origin. Required when a TLS proxy exposes Ion remotely. |
| `--dev-file-kek` | off | `init` | Use the development-only file KEK. Never use in production. |
| `--attach` | off | `tui` | Attach the terminal operator to a running dashboard runtime. |

Run `ion <command> --help` for the authoritative flag list of each command.

## Environment variables

The Makefile and container images read these; the binary honors the data
directory and listen address through them where a flag is not supplied.

| Variable | Default | Description |
|---|---|---|
| `ION_DATA_DIR` | `~/.ion` | Data directory. |
| `ION_WEB_LISTEN` | `127.0.0.1:4174` | Web operator listen address. |
| `ION_WEB_ORIGIN` | listen URL | Exact browser origin, including `https://` for remote deployments. |
| `ION_AUTH_USERNAME` | unset | Operator username. Required with one password variable outside loopback development. |
| `ION_AUTH_PASSWORD` | unset | Operator password, 12-1024 characters. Mutually exclusive with `ION_AUTH_PASSWORD_HASH`. |
| `ION_AUTH_PASSWORD_HASH` | unset | Argon2id v=19 PHC password hash. Mutually exclusive with `ION_AUTH_PASSWORD`. |

## Deployment authentication

Remote Ion dashboards require deployment authentication. Configure
`ION_AUTH_USERNAME` and exactly one of `ION_AUTH_PASSWORD` or
`ION_AUTH_PASSWORD_HASH`. Plaintext deployment passwords are converted to an
Argon2id verifier with a fresh random salt during startup; the password is not
stored in Ion's runtime configuration or durable data. An existing Argon2id PHC
hash can be supplied when the deployment platform supports generating one
before startup.

The login endpoint accepts only bounded same-origin JSON requests. Five failed
attempts within the limiter window pause further attempts for 15 minutes.
Successful login creates a 12-hour signed Secure, HttpOnly, SameSite=Strict
session. Restarting the dashboard invalidates existing sessions. Sign out
removes both the session and CSRF cookies.

Partial or ambiguous credential configuration stops startup. Ion also stops
startup if Railway is detected without credentials, or when a non-loopback
browser origin is configured without authentication. Credentials must be
stored as protected or sealed deployment variables and must never be committed
to an environment file.

Container-only variables consumed by the root appliance image:

| Variable | Default | Description |
|---|---|---|
| `ION_AUTO_INIT` | `1` | Initialize the data directory on first run when it has no wrapped User Key. |
| `ION_DEV_FILE_KEK` | `0` | When `1`, initialize with the development file KEK. |
| `ION_VAULT_KEK` | none | Base64-encoded 32-byte production KEK supplied as a protected deployment variable. |
| `ION_APPLIANCE_DATA_ROOT` | `/data` | Root for Ion, Computer, and appliance state in the single mounted volume. |

## The data directory

Everything Ion persists lives under the data directory:

- Encrypted session state (SQLite with WAL).
- Encrypted memory, work, scheduling, and recovery state.
- Vault material derived from the key source.

Treat the data directory as sensitive. Back it up as a unit, and restrict its
permissions (the container and systemd unit use mode `0700`).

## The key source

Ion derives its vault keys from a protected host key source. The Railway
appliance consumes `ION_VAULT_KEK` from a protected deployment variable and
never writes that value to `/data`. On systems without a supported source, the
development file KEK is an explicit, opt-in fallback for development only. Do
not deploy the file KEK to production. See [SECURITY.md](../SECURITY.md) for the
key hierarchy.

## Networking

Plain HTTP is restricted to loopback in the binary. To reach Ion remotely,
terminate TLS in a reverse proxy that forwards to `127.0.0.1:4174`. See
[deployment](deployment.md).
