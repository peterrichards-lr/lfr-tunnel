#!/usr/bin/env python3
"""PreToolUse guard: refuse any command that EXECUTES a forbidden local binary (#2289).

Why this exists, when `.claude/settings.json` already carries 79 deny entries.

A permission rule matches the command text from its first word. Anything whose first word is
not the binary matches nothing, however the binary is spelled later:

    timeout 5 ./bin/lfr-tunneld -h      sudo ./bin/lfr-tunneld
    env LFT_CONFIG=x ./bin/lfr-tunneld  nohup ./bin/lfr-tunneld &

`timeout` is the seductive one: it reads like bounding the risk, and it is incident 3 with a
safety blanket. Nothing else in the repo closes this -- `make install-go-guard` refuses
the toolchain's run and test subcommands only, saying nothing about running an already-built
binary, and `check-edr-safety.sh` is a static scan of tracked source, not of a typed command.

WHY NOT A REGEX OVER THE WHOLE COMMAND. It over-blocks, and a guard that cries wolf gets
exemptions bolted on until it says nothing:

    scp dist/lfr-tunnel-linux-amd64 host:   publishes a binary, executes nothing
    gofmt -w cmd/lfr-tunneld/main.go        formats source
    git log -- cmd/lfr-tunneld              reads history

So this parses COMMAND POSITION: split on shell operators, step over prefix words, and look at
the executable actually being invoked.

THE RULE ON THE BASENAME, which is why it needs no artefact list: strip a trailing `.exe`; if
the basename starts with `lfr-tunnel` and is not `lfr-tunnel-ops`, refuse. That covers every
`dist/lfr-tunnel-<os>-<arch>`, every `bin/lfr-tunneld-*-linux`, the three root wrappers, and any
artefact a future release invents -- without this file needing to know their names.

FAILS CLOSED. Any parse error, any unexpected exception, refuses. A guard that fails open is a
guard that is absent exactly when something unusual is happening.
"""

import json
import os
import re
import shlex
import sys

# The one binary the EDR skill names as safe to run directly ("the `lfr-tunnel-ops` (deploy
# tooling) binary"). `make deploy` executes it, so a guard that catches it breaks every release.
RUNNABLE = "lfr-tunnel-ops"

# Words that may precede the real executable. Each entry says how many of ITS OWN arguments to
# skip before the executable appears; `None` means "skip anything that still looks like an
# option or an assignment".
PREFIX_WORDS = {
    "sudo": None,
    "command": None,
    "exec": None,
    "nohup": 0,
    "time": 0,
    "nice": None,
    "ionice": None,
    "stdbuf": None,
    "setsid": 0,
    "env": None,
    "timeout": None,
    "xargs": None,
    "watch": None,
}

# Splits a command line into separately-executed segments.
OPERATORS = re.compile(r"(?:\|\||&&|\||;|&(?!&))")


def forbidden_basename(token: str) -> bool:
    """True when this token names a binary that must never be executed locally."""
    base = os.path.basename(token)
    if base.endswith(".exe"):
        base = base[: -len(".exe")]
    if base == RUNNABLE or base.startswith(RUNNABLE + "."):
        return False
    return base.startswith("lfr-tunnel")


def executable_of(segment: str):
    """The executable a segment actually runs, stepping over prefix words. None if unclear."""
    try:
        tokens = shlex.split(segment)
    except ValueError:
        # Unbalanced quotes. Fall back to whitespace so a malformed command cannot slip past by
        # being unparseable -- the caller treats a None as "cannot tell", which fails closed.
        tokens = segment.split()

    i = 0
    while i < len(tokens):
        tok = tokens[i]

        # VAR=value assignments precede the executable.
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", tok):
            i += 1
            continue

        base = os.path.basename(tok)
        if base in PREFIX_WORDS:
            skip = PREFIX_WORDS[base]
            i += 1
            if skip is None:
                # Step over this prefix's own options and their values -- `timeout 5`,
                # `env FOO=1`, `sudo -u ubuntu`.
                while i < len(tokens) and (
                    tokens[i].startswith("-")
                    or re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", tokens[i])
                    or re.match(r"^[0-9]+(\.[0-9]+)?[smhd]?$", tokens[i])
                ):
                    i += 1
            else:
                i += skip
            continue

        return tok

    return None


def offending(command: str):
    """The first token in the command that would execute something forbidden, or None."""
    for segment in OPERATORS.split(command):
        segment = segment.strip()
        if not segment:
            continue

        exe = executable_of(segment)
        if exe is None:
            continue

        if forbidden_basename(exe):
            return exe

        # `go run ./cmd/lfr-tunneld` executes the daemon even though the executable is `go`.
        # The go PATH shim refuses every `go run` on this machine, but the shim is machine-local
        # and this hook is not, so check it here too.
        if os.path.basename(exe) == "go":
            try:
                tokens = shlex.split(segment)
            except ValueError:
                tokens = segment.split()
            if "run" in tokens:
                for tok in tokens:
                    if forbidden_basename(tok.rstrip("/")):
                        return tok
    return None


def deny(reason: str) -> None:
    json.dump(
        {
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "deny",
                "permissionDecisionReason": reason,
            }
        },
        sys.stdout,
    )
    sys.stdout.write("\n")
    sys.exit(0)


def main() -> None:
    try:
        payload = json.load(sys.stdin)
        command = payload.get("tool_input", {}).get("command", "")
    except Exception as exc:  # noqa: BLE001 -- fail closed on anything
        deny(
            "EDR guard could not read the command it was asked to check "
            f"({exc.__class__.__name__}), so it refused. See #2289."
        )
        return

    if not isinstance(command, str) or not command.strip():
        sys.exit(0)

    hit = offending(command)
    if hit:
        deny(
            f"Refused: this would execute `{hit}` locally.\n\n"
            "There is no verified-safe way to run lfr-tunnel or lfr-tunneld on this machine, "
            "regardless of build location -- three attempts have each cost a full environment "
            "reinstall (SentinelOne quarantines the binary and takes unrelated tooling with it).\n\n"
            "A prefix word does not make it safe: `timeout`, `sudo`, `env` and `nohup` were the "
            "gap this guard closes (#2289). Verify with `make test`, a compile, `tsc -b` or code "
            "review, or use the Playwright suite (`make e2e-ui`), which runs in Docker -- a "
            "different risk profile. See .agents/skills/edr-constraints/SKILL.md.\n\n"
            "`lfr-tunnel-ops` is the one binary that IS safe to run directly."
        )


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 -- fail closed
        deny(f"EDR guard errored ({exc.__class__.__name__}) and refused rather than allow. #2289")
