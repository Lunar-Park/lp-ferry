"""``ferry.toml``: the settings the plugin reads, and the keys it refuses.

The search order is the one the CLI uses: an explicit path, then ``./ferry.toml``,
then ``~/.ferry/config.toml``, then the defaults below. A file park cannot read is an
error rather than a silent fallback: a typo would otherwise keep a default quietly in
place, which on a card offload means media landing somewhere nobody expected.

Unknown keys fail. That covers the top level, ``[organize]`` and ``[proxy]``. The
``[proxy]`` table is a convenience form of the three proxy keys at the top level,
which stay canonical.
"""

from __future__ import annotations

import tomllib
from dataclasses import dataclass
from pathlib import Path

FILENAME = "ferry.toml"
HOME_DIR = ".ferry"
HOME_FILE = "config.toml"

DEFAULT_TEMPLATE = "{root}/{source_relpath}/{filename}{ext}"

TOP_LEVEL_KEYS = frozenset(
    {
        "organize",
        "proxy",
        "proxy_codec",
        "proxy_height",
        "proxy_backend",
        "checksum_algo",
        "ffmpeg_path",
        "resolve_path",
    }
)
ORGANIZE_KEYS = frozenset({"template", "on_conflict", "mode"})
PROXY_KEYS = frozenset({"proxy_codec", "proxy_height", "proxy_backend"})
PROXY_BACKENDS = ("auto", "avfoundation", "ffmpeg")
CHECKSUM_ALGOS = ("xxhash", "sha256")
ON_CONFLICT = ("skip", "overwrite", "rename")
MODES = ("copy",)

# Every key a plan needs to render a destination path.
TEMPLATE_FIELDS = (
    "root",
    "source_relpath",
    "filename",
    "ext",
    "date",
    "codec_family",
    "resolution_bucket",
)


class ConfigError(ValueError):
    """``ferry.toml`` holds a key or a value the plugin will not act on."""


@dataclass(frozen=True)
class Config:
    """The settings the plugin acts on, with the CLI's defaults."""

    template: str = DEFAULT_TEMPLATE
    on_conflict: str = "skip"
    proxy_codec: str = "ProRes422Proxy"
    proxy_height: int = 1080
    proxy_backend: str = "auto"
    checksum_algo: str = "xxhash"
    ffmpeg_path: str | None = None
    resolve_path: str | None = None

    def to_dict(self) -> dict:
        return {
            "template": self.template,
            "on_conflict": self.on_conflict,
            "proxy_codec": self.proxy_codec,
            "proxy_height": self.proxy_height,
            "proxy_backend": self.proxy_backend,
            "checksum_algo": self.checksum_algo,
            "ffmpeg_path": self.ffmpeg_path,
            "resolve_path": self.resolve_path,
        }


def find(path: Path | str | None = None) -> Path | None:
    """The config file a call reads, or ``None`` when there is none."""
    if path is not None:
        candidate = Path(path).expanduser()
        if not candidate.is_file():
            raise ConfigError(f"no such ferry config: {candidate}")
        return candidate
    local = Path.cwd() / FILENAME
    if local.is_file():
        return local
    home = Path.home() / HOME_DIR / HOME_FILE
    return home if home.is_file() else None


def load(path: Path | str | None = None) -> Config:
    """Read the config, rejecting anything this version would ignore."""
    found = find(path)
    if found is None:
        return Config()
    try:
        with open(found, "rb") as fh:
            raw = tomllib.load(fh)
    except tomllib.TOMLDecodeError as exc:
        raise ConfigError(f"{found}: {exc}") from exc
    if not isinstance(raw, dict):
        raise ConfigError(f"{found}: not a TOML table")
    return from_dict(raw, source=str(found))


def from_dict(raw: dict, *, source: str = "ferry.toml") -> Config:
    """Validate a parsed config table."""
    data = dict(raw)
    _unknown(data, TOP_LEVEL_KEYS, source, "")

    organize = data.pop("organize", {}) or {}
    if not isinstance(organize, dict):
        raise ConfigError(f"{source}: organize must be a table")
    _unknown(organize, ORGANIZE_KEYS, source, "organize.")

    proxy = data.pop("proxy", {}) or {}
    if not isinstance(proxy, dict):
        raise ConfigError(f"{source}: proxy must be a table")
    _unknown(proxy, PROXY_KEYS, source, "proxy.")
    for key in PROXY_KEYS:
        if key in proxy:
            if key in data:
                raise ConfigError(f"{source}: {key} is set twice, at the top level and in [proxy]")
            data[key] = proxy[key]

    template = organize.get("template", DEFAULT_TEMPLATE)
    if not isinstance(template, str) or not template.strip():
        raise ConfigError(f"{source}: organize.template must be a non-empty string")
    # A template without these placeholders would silently flatten a card into one
    # folder, so they are required rather than checked at the first plan.
    for field in ("root", "filename"):
        if "{" + field + "}" not in template:
            raise ConfigError(f"{source}: organize.template must contain {{{field}}}")
    _only_fields(template, source)

    on_conflict = organize.get("on_conflict", "skip")
    if on_conflict not in ON_CONFLICT:
        raise ConfigError(f"{source}: organize.on_conflict must be one of {', '.join(ON_CONFLICT)}")
    mode = organize.get("mode", "copy")
    if mode not in MODES:
        raise ConfigError(f"{source}: organize.mode = {mode!r} is not supported; the plugin copies")
    proxy_backend = data.get("proxy_backend", "auto")
    if proxy_backend not in PROXY_BACKENDS:
        raise ConfigError(f"{source}: proxy_backend must be one of {', '.join(PROXY_BACKENDS)}")
    checksum_algo = data.get("checksum_algo", "xxhash")
    if checksum_algo not in CHECKSUM_ALGOS:
        raise ConfigError(f"{source}: checksum_algo must be one of {', '.join(CHECKSUM_ALGOS)}")
    proxy_height = data.get("proxy_height", 1080)
    if isinstance(proxy_height, bool) or not isinstance(proxy_height, int) or proxy_height < 16:
        raise ConfigError(f"{source}: proxy_height must be an integer of 16 or more")
    proxy_codec = data.get("proxy_codec", "ProRes422Proxy")
    if not isinstance(proxy_codec, str) or not proxy_codec.strip():
        raise ConfigError(f"{source}: proxy_codec must be a non-empty string")

    return Config(
        template=template,
        on_conflict=on_conflict,
        proxy_codec=proxy_codec,
        proxy_height=proxy_height,
        proxy_backend=proxy_backend,
        checksum_algo=checksum_algo,
        ffmpeg_path=_path_or_none(data.get("ffmpeg_path"), f"{source}: ffmpeg_path"),
        resolve_path=_path_or_none(data.get("resolve_path"), f"{source}: resolve_path"),
    )


def _unknown(table: dict, allowed: frozenset, source: str, prefix: str) -> None:
    unknown = sorted(set(table) - allowed)
    if unknown:
        names = ", ".join(prefix + name for name in unknown)
        raise ConfigError(f"{source}: unknown key {names}")


def _only_fields(template: str, source: str) -> None:
    known = set(TEMPLATE_FIELDS)
    index = 0
    while True:
        start = template.find("{", index)
        if start < 0:
            return
        end = template.find("}", start)
        if end < 0:
            raise ConfigError(f"{source}: organize.template has an unclosed placeholder")
        name = template[start + 1 : end]
        if name not in known:
            raise ConfigError(
                f"{source}: organize.template uses {{{name}}}; "
                f"the placeholders are {', '.join(TEMPLATE_FIELDS)}"
            )
        index = end + 1


def _path_or_none(value: object, where: str) -> str | None:
    if value is None:
        return None
    if not isinstance(value, str) or not value.strip():
        raise ConfigError(f"{where} must be a path string")
    return value


__all__ = [
    "CHECKSUM_ALGOS",
    "DEFAULT_TEMPLATE",
    "TEMPLATE_FIELDS",
    "Config",
    "ConfigError",
    "find",
    "from_dict",
    "load",
]
