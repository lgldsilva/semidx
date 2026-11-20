#!/usr/bin/env python3
"""Structural validation for rendered package manifests (AUR, Snap, winget).

Used by the release workflow's validate-package-manifests job and locally:
  python3 scripts/validate-package-manifests.py <rendered-dir>

Checks structure only (parseability, required keys, checksum formats). It does
not verify the SHA-256 values themselves — the renderer guarantees they came
from the release checksums.txt; the package managers re-verify at install.
"""
import re
import sys
import tempfile
from pathlib import Path

SHA256 = re.compile(r"^[a-f0-9]{64}$")


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    sys.exit(1)


def resolve_root(raw: str) -> Path:
    """Resolve the rendered-dir argument inside a bounded root.

    SonarCloud S7250 (agentic path injection): the script reads whatever the
    CLI argument names, so constrain it to the working directory or the system
    temporary directory. Anything else is refused instead of read.
    """
    root = Path(raw).resolve()
    for base in (Path.cwd().resolve(), Path(tempfile.gettempdir()).resolve()):
        if root.is_relative_to(base):
            return root
    fail(f"{raw}: must live under the working directory or the system temp dir")


def load(path: Path):
    import yaml

    try:
        return yaml.safe_load(path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001 - report any parse failure as a gate
        fail(f"{path}: invalid YAML: {exc}")


def validate_aur(root: Path) -> None:
    pkgbuild = root / "aur" / "PKGBUILD"
    if not pkgbuild.is_file():
        fail(f"{pkgbuild}: missing")
    # Cross-check rendered sha256sums_* against checksums.txt: namcap and
    # makepkg --printsrcinfo do not validate checksum format, so a broken
    # render would otherwise ship in the release manifests untouched.
    checksums = (root.parent / "checksums.txt") if (root.parent / "checksums.txt").is_file() else None
    if checksums is None:
        # The release workflow renders manifests beside checksums.txt; when
        # run standalone (repo root), skip the cross-check gracefully.
        print(f"ok: {pkgbuild} exists (namcap/makepkg run in the release workflow)")
        return
    body = pkgbuild.read_text(encoding="utf-8")
    rendered = {}
    for match in re.finditer(r"sha256sums(?:_\w+)?=\(([^)]*)\)", body, re.DOTALL):
        for token in re.findall(r"['\"]?([0-9a-fA-F]{64})['\"]?", match.group(1)):
            rendered[token.lower()] = True
    if not rendered:
        fail(f"{pkgbuild}: no sha256sums entries found")
    known = {}
    for line in checksums.read_text(encoding="utf-8").splitlines():
        parts = line.split()
        if len(parts) >= 2:
            known[parts[0].lower()] = parts[-1]
    for digest in rendered:
        if digest not in known:
            fail(f"{pkgbuild}: sha256 {digest} not present in {checksums.name}")
    print(f"ok: {pkgbuild} exists, {len(rendered)} sha256sum(s) cross-checked against {checksums.name}")


def validate_snap(root: Path) -> None:
    for arch in ("amd64", "arm64"):
        manifest = root / "snap" / arch / "snapcraft.yaml"
        if not manifest.is_file():
            fail(f"{manifest}: missing")
        doc = load(manifest)
        for key in ("name", "version", "summary", "confinement", "parts"):
            if key not in doc:
                fail(f"{manifest}: missing key {key}")
        if doc["name"] != "semidx":
            fail(f"{manifest}: unexpected snap name {doc['name']}")
        if not SHA256.fullmatch(str(doc["parts"]["semidx"]["source-checksum"]).split("/", 1)[-1]):
            fail(f"{manifest}: malformed source-checksum")
        print(f"ok: {manifest}")


def validate_winget(root: Path) -> None:
    version = root / "winget" / "Semidx.semidx.version.yaml"
    default_locale = root / "winget" / "Semidx.semidx.yaml"
    installer = root / "winget" / "Semidx.semidx.installer.yaml"
    for path in (version, default_locale, installer):
        if not path.is_file():
            fail(f"{path}: missing")

    doc = load(version)
    if doc.get("ManifestType") != "version" or doc.get("ManifestVersion") != "1.6.0":
        fail(f"{version}: bad ManifestType/ManifestVersion")
    if doc.get("PackageIdentifier") != "Semidx.semidx" or not doc.get("PackageVersion"):
        fail(f"{version}: bad PackageIdentifier/PackageVersion")

    doc = load(default_locale)
    if doc.get("ManifestType") != "defaultLocale" or doc.get("ManifestVersion") != "1.6.0":
        fail(f"{default_locale}: bad ManifestType/ManifestVersion")
    for key in ("Publisher", "PackageName", "License", "ShortDescription"):
        if not doc.get(key):
            fail(f"{default_locale}: missing key {key}")

    doc = load(installer)
    if doc.get("ManifestType") != "installer" or doc.get("ManifestVersion") != "1.6.0":
        fail(f"{installer}: bad ManifestType/ManifestVersion")
    installers = doc.get("Installers") or []
    archs = {i.get("Architecture") for i in installers}
    if archs != {"x64", "arm64"}:
        fail(f"{installer}: expected x64+arm64 installers, got {sorted(filter(None, archs))}")
    for entry in installers:
        label = entry.get("Architecture")
        if not SHA256.fullmatch(str(entry.get("InstallerSha256", ""))):
            fail(f"{installer}: malformed InstallerSha256 for {label}")
        if entry.get("InstallerType") != "zip" or entry.get("NestedInstallerType") != "portable":
            fail(f"{installer}: expected zip/portable installer for {label}")
        files = entry.get("NestedInstallerFiles") or []
        aliases = {f.get("PortableCommandAlias") for f in files}
        if not {"semidx", "chatrag"} <= aliases:
            fail(f"{installer}: missing portable command aliases for {label}")
        for f in files:
            if not f.get("RelativeFilePath"):
                fail(f"{installer}: missing RelativeFilePath for {label}")
        print(f"ok: {installer} ({label})")


def main() -> None:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} <rendered-dir>", file=sys.stderr)
        sys.exit(64)
    root = resolve_root(sys.argv[1])
    if not root.is_dir():
        fail(f"{root}: not a directory")
    validate_aur(root)
    validate_snap(root)
    validate_winget(root)
    print("all package manifests valid")


if __name__ == "__main__":
    main()
