#!/usr/bin/env python3
"""LICENSE・NOTICE が同梱の OSS・提供データの出典と合っているかを確かめる（T-009 / Issue #17）。

使い方:
    python3 scripts/check-license.py [--root DIR]

DIR の既定はリポジトリのルート（このスクリプトの 1 つ上）。ファイルは DIR から直接読む
（git を使わない）。確かめること:

    L-1  LICENSE が Apache License 2.0 の本文そのもの（SHA-256 で比べる）
    L-2  NOTICE があり、空でなく、Apache License, Version 2.0 を含む
    L-3  server/third_party/<名前>/ に LICENSE があり、NOTICE がそのパスを含む。
         etcd-raft は LICENSE が Apache-2.0 の本文・PATCHES.md があり NOTICE が含む・
         server/go.mod の go.etcd.io/raft/v3 の版を NOTICE が含む
    L-4  data/islands.json の sources の title・license・termsUrl を NOTICE が含み、
         termsUrl を data/raw/README.md が含む
    L-5  NOTICE が web/public/assets/CREDITS.md を含む。web/public/assets/generated/ に
         .gitkeep 以外のファイルがあれば、CREDITS.md があり、各ファイル名を含む

NOTICE などとの比較は、空白・改行をすべて取り除いた文字列どうしの部分文字列の判定で行う。
問題はすべて `NG L-<n>: <パス>: <内容>` の形で標準出力に出し、1 件でもあれば終了コード 1。
問題がなければ終了コード 0。--root がディレクトリでない・引数が不正なときは終了コード 2。
標準ライブラリだけを使う（Python 3.9 以上）。
"""

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path
from typing import List, Optional, Tuple

# apache.org の LICENSE-2.0.txt の SHA-256（main の LICENSE と server/third_party/etcd-raft/LICENSE も同じ値）
APACHE_2_0_SHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

LICENSE_NAMES = ("LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING")

THIRD_PARTY = "server/third_party"
ETCD_RAFT = "etcd-raft"
RAFT_MODULE = "go.etcd.io/raft/v3"
CREDITS = "web/public/assets/CREDITS.md"
GENERATED = "web/public/assets/generated"

WS_RE = re.compile(r"\s+")


def normalize(text: str) -> str:
    """空白・改行をすべて取り除く。"""
    return WS_RE.sub("", text)


def contains(haystack: str, needle: str) -> bool:
    return normalize(needle) in normalize(haystack)


class Report:
    def __init__(self) -> None:
        self.errors: List[str] = []

    def ng(self, check: str, path: str, message: str) -> None:
        self.errors.append(f"NG {check}: {path}: {message}")


def read_bytes(root: Path, rel: str) -> Optional[bytes]:
    try:
        return (root / rel).read_bytes()
    except OSError:
        return None


def read_text(root: Path, rel: str) -> Optional[str]:
    data = read_bytes(root, rel)
    if data is None:
        return None
    return data.decode("utf-8", errors="replace")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def check_license(root: Path, report: Report) -> None:
    """L-1: LICENSE が Apache License 2.0 の本文そのもの。"""
    data = read_bytes(root, "LICENSE")
    if data is None:
        report.ng("L-1", "LICENSE", "ない・読めない（Apache License 2.0 の本文 LICENSE-2.0.txt をそのまま置く）")
        return
    if sha256(data) != APACHE_2_0_SHA256:
        report.ng(
            "L-1",
            "LICENSE",
            "Apache License 2.0 の本文（apache.org の LICENSE-2.0.txt）と違う（本文を変えずにそのまま置く）",
        )


def check_notice_basic(notice: Optional[str], report: Report) -> bool:
    """L-2: NOTICE があり、空でなく、Apache License, Version 2.0 を含む。

    NOTICE の中身で比べる後の確認を行えるとき True を返す。
    """
    if notice is None:
        report.ng("L-2", "NOTICE", "ない・読めない（ADR-0020。ルートに NOTICE を置く）")
        return False
    if not notice.strip():
        report.ng("L-2", "NOTICE", "空（ADR-0020。著作権・同梱の OSS・提供データの出典を書く）")
        return False
    if not contains(notice, "Apache License, Version 2.0"):
        report.ng("L-2", "NOTICE", "「Apache License, Version 2.0」を含まない（このソフトウェアのライセンスを書く）")
    return True


def parse_go_mod_require(text: str, module: str) -> Optional[str]:
    """go.mod の require から module の版を返す（なければ None）。replace は見ない。"""
    block = ""
    for raw in text.splitlines():
        line = raw.split("//", 1)[0].strip()
        if not line:
            continue
        if block:
            if line == ")":
                block = ""
                continue
            if block == "require":
                fields = line.split()
                if len(fields) >= 2 and fields[0] == module:
                    return fields[1]
            continue
        fields = line.split()
        if len(fields) == 2 and fields[1] == "(":
            block = fields[0]
            continue
        if fields[0] == "require" and len(fields) >= 3 and fields[1] == module:
            return fields[2]
    return None


def find_license_file(root: Path, rel_dir: str) -> Optional[str]:
    for name in LICENSE_NAMES:
        if (root / rel_dir / name).is_file():
            return f"{rel_dir}/{name}"
    return None


def check_third_party(root: Path, notice: Optional[str], report: Report) -> None:
    """L-3: 同梱の OSS（server/third_party/）。"""
    tp = root / THIRD_PARTY
    names: List[str] = []
    if tp.is_dir():
        names = sorted(p.name for p in tp.iterdir() if p.is_dir())
    if ETCD_RAFT not in names:
        report.ng("L-3", f"{THIRD_PARTY}/{ETCD_RAFT}", "ない（同梱の etcd-io/raft。ADR-0005）")

    for name in names:
        rel_dir = f"{THIRD_PARTY}/{name}"
        lic = find_license_file(root, rel_dir)
        if lic is None:
            report.ng(
                "L-3",
                rel_dir,
                "LICENSE がない（LICENSE・LICENSE.txt・LICENSE.md・COPYING のどれかを、上流のものそのままで置く）",
            )
        if notice is not None and not contains(notice, f"{rel_dir}/"):
            report.ng("L-3", "NOTICE", f"{rel_dir}/ を含まない（同梱した OSS の名前・著作権・ライセンス・置き場所を NOTICE に書く）")
        if name != ETCD_RAFT:
            continue
        if lic is not None:
            data = read_bytes(root, lic)
            if data is None or sha256(data) != APACHE_2_0_SHA256:
                report.ng("L-3", lic, "Apache License 2.0 の本文と違う（上流の etcd-io/raft の LICENSE をそのまま置く）")
        patches = f"{rel_dir}/PATCHES.md"
        if not (root / patches).is_file():
            report.ng("L-3", patches, "ない（上流からの変更の記録。ADR-0005）")
        if notice is not None and not contains(notice, patches):
            report.ng("L-3", "NOTICE", f"{patches} を含まない（変更の記録の場所を NOTICE に書く）")

    go_mod = read_text(root, "server/go.mod")
    if go_mod is None:
        report.ng("L-3", "server/go.mod", "ない・読めない（etcd-io/raft の版を確かめられない）")
        return
    version = parse_go_mod_require(go_mod, RAFT_MODULE)
    if version is None:
        report.ng("L-3", "server/go.mod", f"{RAFT_MODULE} の require がない（etcd-io/raft の版を確かめられない）")
        return
    if notice is not None and not contains(notice, f"{RAFT_MODULE} {version}"):
        report.ng(
            "L-3",
            "NOTICE",
            f"「{RAFT_MODULE} {version}」を含まない（NOTICE の etcd-io/raft の版を server/go.mod に合わせる）",
        )


def load_sources(root: Path, report: Report) -> List[Tuple[str, dict]]:
    rel = "data/islands.json"
    text = read_text(root, rel)
    if text is None:
        report.ng("L-4", rel, "ない・読めない（提供データの出典を確かめられない）")
        return []
    try:
        data = json.loads(text)
    except ValueError as e:
        report.ng("L-4", rel, f"JSON として読めない（{e}。make の ETL で作り直す）")
        return []
    sources = data.get("sources") if isinstance(data, dict) else None
    if isinstance(sources, dict):
        items = [(str(k), v) for k, v in sources.items()]
    elif isinstance(sources, list):
        items = [(str(i), v) for i, v in enumerate(sources)]
    else:
        report.ng("L-4", rel, "sources がない（提供データの出典を書く。docs/07-data-spec.md）")
        return []
    if not items:
        report.ng("L-4", rel, "sources が空（提供データの出典を書く。docs/07-data-spec.md）")
    return items


def check_sources(root: Path, notice: Optional[str], report: Report) -> None:
    """L-4: 提供データの出典。"""
    items = load_sources(root, report)
    if not items:
        return
    raw_rel = "data/raw/README.md"
    raw_readme = read_text(root, raw_rel)
    if raw_readme is None:
        report.ng("L-4", raw_rel, "ない・読めない（提供データの出典と利用規約を書く）")
    for key, src in items:
        if not isinstance(src, dict):
            report.ng("L-4", "data/islands.json", f"sources の {key} がオブジェクトでない")
            continue
        for field in ("title", "license", "termsUrl"):
            value = src.get(field)
            if not isinstance(value, str) or not value.strip():
                report.ng("L-4", "data/islands.json", f"sources の {key} に {field} がない")
                continue
            if notice is not None and not contains(notice, value):
                report.ng(
                    "L-4",
                    "NOTICE",
                    f"sources の {key} の {field}「{value}」を含まない（Open data の節に出典・利用規約を書く）",
                )
        terms = src.get("termsUrl")
        if raw_readme is not None and isinstance(terms, str) and terms.strip() and not contains(raw_readme, terms):
            report.ng("L-4", raw_rel, f"sources の {key} の termsUrl「{terms}」を含まない（利用規約の URL を書く）")


def list_generated(root: Path) -> List[str]:
    gen = root / GENERATED
    if not gen.is_dir():
        return []
    return sorted(p.name for p in gen.rglob("*") if p.is_file() and p.name != ".gitkeep")


def check_assets(root: Path, notice: Optional[str], report: Report) -> None:
    """L-5: 生成した素材の記載先。"""
    if notice is not None and not contains(notice, CREDITS):
        report.ng("L-5", "NOTICE", f"{CREDITS} を含まない（生成した素材の記載先を書く。docs/07-data-spec.md §7）")
    files = list_generated(root)
    if not files:
        return
    credits = read_text(root, CREDITS)
    if credits is None or not credits.strip():
        report.ng(
            "L-5",
            CREDITS,
            f"ない・空（{GENERATED}/ に素材があるので、素材ごとのサービス・作成日・利用規約を書く）",
        )
        return
    for name in files:
        if not contains(credits, name):
            report.ng("L-5", CREDITS, f"{name} を含まない（{GENERATED}/ の素材ごとに記載する）")


def check(root: Path) -> List[str]:
    report = Report()
    check_license(root, report)
    notice_text = read_text(root, "NOTICE")
    usable = check_notice_basic(notice_text, report)
    notice = notice_text if usable else None
    check_third_party(root, notice, report)
    check_sources(root, notice, report)
    check_assets(root, notice, report)
    return report.errors


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        prog="check-license.py",
        description="LICENSE・NOTICE が同梱の OSS・提供データの出典と合っているかを確かめる",
    )
    parser.add_argument("--root", help="リポジトリのルート（既定: このスクリプトの 1 つ上）")
    try:
        args = parser.parse_args(argv)
    except SystemExit as e:
        return 2 if e.code else 0
    root = Path(args.root) if args.root else Path(__file__).resolve().parent.parent
    if not root.is_dir():
        print(f"check-license: ディレクトリでない: {root}", file=sys.stderr)
        return 2
    errors = check(root)
    for e in errors:
        print(e)
    if errors:
        print(f"check-license: 問題が {len(errors)} 件ある", file=sys.stderr)
        return 1
    print("OK: LICENSE・NOTICE（L-1〜L-5）")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
