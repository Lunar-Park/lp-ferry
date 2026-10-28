"""ferry.toml: the keys the plugin reads and the ones it refuses."""

from __future__ import annotations

from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import config


def test_defaults_match_the_shipped_example():
    settings = config.from_dict({})
    assert settings.template == "{root}/{source_relpath}/{filename}{ext}"
    assert settings.on_conflict == "skip"
    assert settings.proxy_codec == "ProRes422Proxy"
    assert settings.proxy_height == 1080
    assert settings.proxy_backend == "auto"
    assert settings.checksum_algo == "xxhash"
    example = Path(__file__).resolve().parents[2] / "ferry.toml.example"
    text = example.read_text(encoding="utf-8")
    for key in ("proxy_codec", "proxy_height", "proxy_backend", "checksum_algo", "organize"):
        assert key in text


def test_an_unknown_top_level_key_is_refused():
    with pytest.raises(config.ConfigError, match="unknown key checksum"):
        config.from_dict({"checksum": "xxhash"})


def test_an_unknown_organize_key_is_refused():
    with pytest.raises(config.ConfigError, match=r"unknown key organize\.templat"):
        config.from_dict({"organize": {"templat": "{root}/{filename}{ext}"}})


def test_an_unknown_proxy_key_is_refused():
    with pytest.raises(config.ConfigError, match=r"unknown key proxy\.heigth"):
        config.from_dict({"proxy": {"heigth": 720}})


def test_the_proxy_sub_table_promotes_its_keys():
    settings = config.from_dict({"proxy": {"proxy_codec": "ProRes422LT"}})
    assert settings.proxy_codec == "ProRes422LT"


def test_a_key_set_twice_is_refused():
    with pytest.raises(config.ConfigError, match="set twice"):
        config.from_dict({"proxy_height": 720, "proxy": {"proxy_height": 1080}})


def test_a_template_without_a_filename_is_refused():
    with pytest.raises(config.ConfigError, match="filename"):
        config.from_dict({"organize": {"template": "{root}/{source_relpath}"}})


def test_a_template_with_an_unknown_placeholder_is_refused():
    with pytest.raises(config.ConfigError, match="camera"):
        config.from_dict({"organize": {"template": "{root}/{camera}/{filename}{ext}"}})


def test_move_is_refused_because_the_plugin_copies():
    with pytest.raises(config.ConfigError, match="copies"):
        config.from_dict({"organize": {"mode": "move"}})


def test_bad_values_are_refused():
    with pytest.raises(config.ConfigError, match="on_conflict"):
        config.from_dict({"organize": {"on_conflict": "clobber"}})
    with pytest.raises(config.ConfigError, match="proxy_backend"):
        config.from_dict({"proxy_backend": "quicktime"})
    with pytest.raises(config.ConfigError, match="checksum_algo"):
        config.from_dict({"checksum_algo": "md5"})
    with pytest.raises(config.ConfigError, match="proxy_height"):
        config.from_dict({"proxy_height": 0})


def test_load_reads_the_file_it_is_given(tmp_path):
    path = tmp_path / "ferry.toml"
    path.write_text('proxy_height = 720\n[organize]\non_conflict = "rename"\n', encoding="utf-8")
    settings = config.load(path)
    assert settings.proxy_height == 720
    assert settings.on_conflict == "rename"


def test_a_missing_explicit_config_is_an_error(tmp_path):
    with pytest.raises(config.ConfigError, match="no such ferry config"):
        config.load(tmp_path / "absent.toml")


def test_broken_toml_is_an_error_not_a_fallback(tmp_path):
    path = tmp_path / "ferry.toml"
    path.write_text("proxy_height = \n", encoding="utf-8")
    with pytest.raises(config.ConfigError):
        config.load(path)


def test_the_plan_refuses_a_bad_config_before_it_touches_the_card(call, card, tmp_path):
    path = tmp_path / "ferry.toml"
    path.write_text("unknown_field = true\n", encoding="utf-8")
    with pytest.raises(Exception) as caught:
        call(
            "ferry.plan",
            {
                "source": str(card.path),
                "destination": str(tmp_path / "out"),
                "config_path": str(path),
            },
        )
    assert "unknown key" in str(caught.value)
