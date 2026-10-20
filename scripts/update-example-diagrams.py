#!/usr/bin/env python3
"""Regenerate README diagrams from the compiled example machine definitions."""

import argparse
import pathlib
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail on diagram drift")
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent.parent
    stale = []
    for source_path in sorted((root / "examples").glob("*/main.go")):
        source = source_path.read_text()
        # Keep the original remainder compiled, including its imports and helpers.
        # Return immediately after Build so services and demo side effects never run.
        marker = "\t\tBuild()"
        if source.count(marker) != 1:
            raise RuntimeError(f"expected one machine Build in {source_path}")
        injected = '''
        if true {
            fmt.Println("GSTATE_MERMAID_BEGIN")
            fmt.Print(gstate.ToMermaid(machine))
            fmt.Println("GSTATE_SCXML_BEGIN")
            text, err := gstate.ToSCXMLString(machine)
            if err != nil { panic(err) }
            fmt.Print(text)
            fmt.Println("GSTATE_EXPORT_END")
            return
        }
'''
        modified = source.replace(marker, marker + injected, 1)
        with tempfile.TemporaryDirectory(prefix="gstate-example-export-") as folder:
            program = pathlib.Path(folder) / "main.go"
            program.write_text(modified)
            output = subprocess.check_output(
                ["go", "run", str(program)], cwd=root, text=True
            )
        mermaid = output.split("GSTATE_MERMAID_BEGIN\n", 1)[1].split(
            "GSTATE_SCXML_BEGIN\n", 1
        )[0]
        scxml = output.split("GSTATE_SCXML_BEGIN\n", 1)[1].split(
            "GSTATE_EXPORT_END\n", 1
        )[0]
        readme = source_path.with_name("README.md")
        old = readme.read_text()
        new, count = re.subn(
            r"```mermaid\n.*?```", lambda _: "```mermaid\n" + mermaid + "```",
            old, count=1, flags=re.S,
        )
        if count != 1:
            raise RuntimeError(f"missing Mermaid block in {readme}")
        new, count = re.subn(
            r"```xml\n.*?```", lambda _: "```xml\n" + scxml + "```",
            new, count=1, flags=re.S,
        )
        if count != 1:
            raise RuntimeError(f"missing SCXML block in {readme}")
        if new != old:
            stale.append(str(readme.relative_to(root)))
            if not args.check:
                readme.write_text(new)
        print(f"compiled {source_path.relative_to(root)}")
    if args.check and stale:
        raise SystemExit("stale diagrams: " + ", ".join(stale))


if __name__ == "__main__":
    main()
