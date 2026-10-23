# Troubleshooting

Start with these host commands from your project's settings directory:

```bash
watermelon doctor
watermelon status
watermelon logs
```

`doctor` works anywhere. Check `status` for the selected project and recorded
policy. Managed logs require a running VM; empty logs do not prove network
access. Before rebuilding, preserve needed VM-local system files. Host source
and managed disks survive normal recreation.

## Installation issues

| Symptom | Action |
|---------|--------|
| `watermelon` not found | Check the binary's install directory is on the assistant/shell `PATH` |
| `limactl` missing or too old | Install stable Lima 2.0.0+; on macOS, `brew install lima` |
| Host/backend check fails | macOS needs 13+ and VZ; Linux needs QEMU matching Lima's architecture |
| `curl` or SSH missing | Install curl and your platform's OpenSSH client |
| Go missing | Only Go/source installs need Go; the shell installer uses prebuilt binaries |
| KVM warning | QEMU can use slower emulation; enable KVM acceleration if available |
| Ask interceptor unavailable | Check GitHub access or supply a matching sidecar; see [sidecar setup](INSTALLATION.md#the-ask-mode-network-interceptor) |

For custom Linux QEMU paths, set `QEMU_SYSTEM_AARCH64` or `QEMU_SYSTEM_X86_64`
using Lima's shell-word syntax. If macOS detection fails, inspect
`sw_vers -productVersion`. See [installation requirements](INSTALLATION.md#requirements).

## VM issues

**VM will not start:** inspect `watermelon status`, `watermelon list`, and
`limactl list`. First creation downloads images and can take several minutes.
If provisioning is incomplete, Watermelon refuses entry and stops the owned
VM. Review the printed error, mirror access, host DNS/VPN, Lima hostname
overrides, and configured scripts. Follow the exact retry/recreation commands
printed by the CLI.

**Stale or unverified policy:** `status` compares recorded configuration, not
the live firewall. Apply reviewed changes with `watermelon rebuild`, or use
the printed VM-pinned recreation instruction. Stop/start alone does not update
policy. For manual `ask`, keep recreation's `run` foreground.

**Missing or invalid settings:** normal `--name` use still validates config.
From the owning root, verified recovery can use:

```bash
watermelon stop --name my-project-vm
watermelon destroy --name my-project-vm
```

Use Watermelon recovery rather than direct Lima deletion, which bypasses
ownership cleanup and can leave names reserved.

**System disk full:** increase `[resources].disk` and rebuild. This does not
resize the separately retained managed workspace disk. Only
`destroy --delete-workspace` permanently deletes that disk and its source.

## Command issues

| Symptom | Action |
|---------|--------|
| Wrong/missing config | Check `status`'s `Project:` path; run from the intended root/settings directory. A nearer invalid config never falls back to a parent. Do not run `init` just to reconnect |
| Tool missing, automatic profile | Run `up`; use `up --refresh` if dependency directories were removed without manifest changes |
| Tool missing, manual config | Declare its command in `[tools]`; global CLIs belong in `[provision]`, then recreate to generate wrappers |
| Wrong subdirectory/Python environment | Use `exec --cwd DIRECTORY`; manual wrappers need the tool's directory option |
| Guest workdir missing | Create the configured directory before selecting it; a manual provision script can create it with `watermelon:watermelon` ownership |
| Provision script unreadable/changed | Restore the valid host file or review the config change and recreate; scripts remain exact policy inputs |
| `sudo` denied | Expected for workloads; declare tools/packages in config |
| Command hangs | Check blocked network access, program input requirements, or missing ask controller |

Automatic profiles cannot combine with global provision package lists or
per-process rules. See [configuration](CONFIG_SPEC.md) and
[assistant setup](CODING_ASSISTANTS.md) for integration failures.

## Network issues

**Downloads fail:** inspect `watermelon logs`. In default `fail`, events show
blocked traffic; in `log`, they show allowed IPv4 traffic. `silent` records
none, and `ask` uses prompts. Logging is rate-limited and does not capture
log-mode IPv6.

Add only trusted, required destinations to the existing `[network].allow`,
then apply the reviewed change with `watermelon rebuild`.

| Standard registry | Typical destinations |
|-------------------|----------------------|
| npm | `registry.npmjs.org` |
| pip | `pypi.org`, `files.pythonhosted.org` |
| Cargo sparse registry | `index.crates.io`, `static.crates.io` |
| Go public proxy | `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` |

Git dependencies, install hooks, and alternate registries may need other
hosts. Wildcards exclude the apex. Exact domain addresses are captured at
bootstrap; rebuild if they have changed. See [network rules](CONFIG_SPEC.md#network).

**Ask controller address in use:** wait for the existing `run`, `exec`, or
`code` controller to exit. Each VM supports one at a time. Direct SSH starts
none; `run --no-shell` is unavailable. Automatic `up` does not support `ask`.

**Always Allow made policy stale:** expected. It allows the current endpoint
across processes, but saves a broader bare-host global rule. Finish the
session, preserve VM-local files, and run the exact recreation command printed
by Watermelon. See [saved-decision scope](CONFIG_SPEC.md#security).

## Port issues

The app must listen on `0.0.0.0` at the same port listed in `[ports].forward`.
Keep it running and test from another host terminal:

```bash
watermelon exec -- curl -I http://127.0.0.1:3000
curl -I http://127.0.0.1:3000
```

If the guest check fails, check the app. If only the host check fails, review
forwarding and rebuild after changes. In `ask`, run the guest check in the
existing shell; a second controller cannot start. If the port is busy, select
another for both the app and the forward. See [dev server setup](WORKSPACES.md#run-a-development-server).

## Performance issues

Increase `[resources].memory` or `cpus` for demanding builds and apply with a
rebuild. Host-mounted files have filesystem overhead; a guest workspace can
avoid that for large dependency trees. Linux without KVM uses slower emulation.

## Getting help

[Open an issue](https://github.com/SomosPollo/watermelon/issues) with the error,
config, and `doctor`/`status` output. Remove secrets and credentials first.
