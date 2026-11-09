#!/usr/bin/env python3
"""Fail when a contract copy pinned in this repo differs from the producer's develop.

A consumer contract test validates against a COPY of the producer's spec. A copy nobody refreshes keeps the test
green while the producer moves on. Copy this file to scripts/harness/pinned_contract_gate.py, add
contracts/pinned.json:
  {"pins": [{"local": "<path of the copy>", "repo": "IQVO/<producer>", "path": "apis/openapi.yaml", "ref": "develop"}]}
and a CI job (checkout, `GH_TOKEN: ${{ github.token }}`, `python3 scripts/harness/pinned_contract_gate.py`; the producer
repos are public). Add a unit test for `compare` (equal -> [], differing -> report, trailing newline counts, long diff
truncated) and run it from the same job and the Makefile's harness-test target.
Exit 0 = every pin equals its producer, 1 = a pin is stale, 2 = a producer could not be read (an unreadable producer
is NOT a pass: a gate that cannot look must say so).
"""
import argparse, difflib, json, os, subprocess, sys


def fetch(repo, path, ref):
    r = subprocess.run(["gh", "api", "-H", "Accept: application/vnd.github.raw", f"repos/{repo}/contents/{path}?ref={ref}"],
                       capture_output=True, text=True)
    if r.returncode:
        raise RuntimeError(f"cannot read {repo}:{path}@{ref}: {r.stderr.strip()[:160]}")
    return r.stdout


def compare(pin, local_text, producer_text):
    if local_text == producer_text:
        return []
    diff = list(difflib.unified_diff(local_text.splitlines(), producer_text.splitlines(),
                                     fromfile=f"pinned {pin['local']}", tofile=f"{pin['repo']}:{pin['path']}@{pin['ref']}",
                                     lineterm="", n=0))
    more = [f"... {len(diff) - 30} more diff lines"] if len(diff) > 30 else []
    return [f"STALE PIN {pin['local']}", f"  WHAT: differs from {pin['repo']} {pin['path']} on {pin['ref']} ({len(diff)} diff lines)",
            "  FIX:  copy the producer's file over the pin, update the provenance note, fix what fails", *diff[:30], *more]


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo-root", default=".")
    a = ap.parse_args(argv)
    pins = json.load(open(os.path.join(a.repo_root, "contracts", "pinned.json")))["pins"]
    bad, unreadable = [], []
    for pin in pins:
        local = open(os.path.join(a.repo_root, pin["local"])).read()
        try:
            bad += compare(pin, local, fetch(pin["repo"], pin["path"], pin["ref"]))
        except RuntimeError as e:
            unreadable.append(str(e))
    if unreadable:
        print("pinned-contract-gate: " + "; ".join(unreadable))
        return 2
    if bad:
        print("\n".join(bad))
        return 1
    print(f"pinned-contract-gate: {len(pins)} pin(s) equal their producer's branch")
    return 0


if __name__ == "__main__":
    sys.exit(main())
