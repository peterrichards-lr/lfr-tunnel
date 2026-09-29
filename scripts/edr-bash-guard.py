#!/usr/bin/env python3
"""PreToolUse guard: refuse any command that EXECUTES a forbidden local binary (#2289).

Why this exists, when `.claude/settings.json` already carries 79 deny entries.

A permission rule matches the command text from its first word. Anything whose first word is
not the binary matches nothing, however the binary is spelled later. The seductive one is a
timeout prefix: it reads like bounding the risk, and it is incident 3 with a safety blanket.
Nothing else in the repo closes this -- the go PATH shim covers the toolchain's run and test
subcommands only, saying nothing about running an already-built binary, and check-edr-safety.sh
is a static scan of tracked source, not of a typed command.

WHY NOT A REGEX OVER THE WHOLE COMMAND. It over-blocks, and a guard that cries wolf gets
exemptions bolted on until it says nothing. Publishing a binary, formatting its source and
verifying its signature are all on the release path and all mention it without running it.

So this parses COMMAND POSITION -- with one deliberate exception. Where position cannot be
determined reliably (a prefix word is present), every token in the segment is checked instead.
That over-blocks slightly and is the correct direction: the cost is a refused copy command, and
the cost of the alternative is the daemon running.

THE RULE ON THE BASENAME, which is why it needs no artefact list: strip a trailing `.exe`; if
the basename starts with the project prefix and is not the ops tool, refuse. That covers every
per-platform client artefact, every suffixed linux daemon build, the three root wrappers, and
anything a future release invents -- without this file needing to know their names.

FAILS CLOSED. Any parse error, any unexpected exception, refuses. The hook wrapper in
settings.json also refuses when this script is missing OR when the interpreter cannot run it --
a corrupted-but-present guard is the shape the 2026-09-09 remediation took.

KNOWN FALSE POSITIVE, stated because it bit the author immediately: a Bash command whose BODY
documents a forbidden command -- a heredoc writing this very file, say -- is refused, because the
body is part of the command string and this guard cannot tell data from instruction. Write such
content with a file tool rather than a shell heredoc.
"""

import json
import os
import re
import shlex
import sys

# The one binary the EDR skill names as safe to run directly. `make deploy` executes it, so a
# guard that catches it breaks every release.
RUNNABLE = "lfr-tunnel-ops"

# Words after which the real command follows. Seeing one means this segment's executable cannot
# be pinpointed reliably: a sudo form with a separated user put that user in command position,
# and a timeout form with a separated signal put the signal there, because a short option's
# separated VALUE looks like neither an option nor a number. So once one is seen, EVERY
# remaining token is checked.
PREFIX_WORDS = {
    "sudo", "command", "exec", "eval", "nohup", "time", "nice", "ionice", "stdbuf",
    "setsid", "env", "timeout", "xargs", "watch", "script", "open",
    # An interpreter handed a wrapper executes it just as surely as the bare path does, and is
    # the natural retry after the bare form is refused.
    "bash", "sh", "zsh", "dash", "ksh", "source", ".",
}

# Separately-executed segments. A NEWLINE is one of them: a multi-line block is the ordinary way
# to write "build, then check it", and without this only the FIRST LINE was inspected -- a build
# line followed by the daemon walked straight through.
OPERATORS = re.compile(r"(?:\|\||&&|\||;|&(?!&)|\n)")

# Tokens that begin a nested command or redirect one: a substitution, a subshell, a brace group,
# a redirection. Stripped before choosing the executable, because otherwise a subshell reads its
# executable as the opening paren and a leading redirect reads it as the angle bracket.
LEADING_NOISE = re.compile(r"^(?:[(){}`]|\$\()$")

# A redirection. Its TARGET is the following token and is not a command, so both are dropped.
REDIRECT = re.compile(r"^[0-9]*[<>]{1,2}&?$")


def forbidden_basename(token):
    """True when this token names a binary that must never be executed locally."""
    base = os.path.basename(token.strip("`'\"(){}"))
    if base.endswith(".exe"):
        base = base[: -len(".exe")]
    # Prefix, not equality: a cross-compiled ops artefact is still the deploy tool. The Makefile
    # writes only the bare name today, but this file's selling point is covering what a future
    # release invents, and refusing that one would break every deploy.
    if base == RUNNABLE or base.startswith(RUNNABLE + "-") or base.startswith(RUNNABLE + "."):
        return False
    return base.startswith("lfr-tunnel")


def tokenize(segment):
    try:
        return shlex.split(segment)
    except ValueError:
        # Unbalanced quotes: fall back to whitespace rather than give up, so a malformed command
        # cannot pass simply by being unparseable.
        return segment.split()


def offending(command):
    """The first token in the command that would execute something forbidden, or None."""
    for segment in OPERATORS.split(command):
        segment = segment.strip()
        if not segment:
            continue

        # Drop grouping/substitution tokens, and drop a redirection TOGETHER WITH ITS TARGET.
        # Filtering the redirect alone left the target in command position, so a form like
        # `> /tmp/out <binary>` stopped at `/tmp/out` and never looked further.
        raw = tokenize(segment)
        tokens = []
        skip_next = False
        for t in raw:
            if skip_next:
                skip_next = False
                continue
            if REDIRECT.match(t):
                skip_next = True          # the next token is the redirect's target, not a command
                continue
            if LEADING_NOISE.match(t):
                continue
            tokens.append(t)
        if not tokens:
            continue

        bases = [os.path.basename(t) for t in tokens]

        # The toolchain, checked across the WHOLE segment rather than at command position: a
        # timeout prefix put its own numeric argument in command position and left the toolchain
        # uninspected entirely. The run subcommand links AND executes from GOTMPDIR; a bare test
        # subcommand compiles an unsigned test binary into the default temp dir and runs it --
        # incident 1, and the one rule the skill puts in a CAUTION block. With -c it compiles
        # without executing, and the Makefile depends on that form, so it stays allowed.
        if "go" in bases:
            if "run" in tokens:
                return "the toolchain's run subcommand"
            if "test" in tokens and "-c" not in tokens:
                return "the toolchain's test subcommand"

        # A prefix word anywhere means command position is unreliable -- check every token.
        if any(b in PREFIX_WORDS for b in bases):
            for tok in tokens:
                if forbidden_basename(tok):
                    return tok
            continue

        # Otherwise the executable is the first token that is not a VAR=value assignment.
        for tok in tokens:
            if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", tok):
                continue
            if forbidden_basename(tok):
                return tok
            break

    return None


def deny(reason):
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


def main():
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
            "There is no verified-safe way to run the client or the daemon on this machine, "
            "regardless of build location -- three attempts have each cost a full environment "
            "reinstall (SentinelOne quarantines the binary and takes unrelated tooling with it).\n\n"
            "A prefix word does not make it safe: that was the gap this guard closes (#2289). "
            "Verify with `make test`, a compile, `tsc -b` or code review, or use the Playwright "
            "suite (`make e2e-ui`), which runs in Docker -- a different risk profile. See "
            ".agents/skills/edr-constraints/SKILL.md.\n\n"
            "If you are WRITING content that merely mentions one of these commands, use a file "
            "tool rather than a shell heredoc: this guard cannot tell a command from a document "
            "about one.\n\n"
            "`lfr-tunnel-ops` is the one binary that IS safe to run directly."
        )


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 -- fail closed
        deny(f"EDR guard errored ({exc.__class__.__name__}) and refused rather than allow. #2289")
