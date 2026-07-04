"""Совместимость py3xui 0.7.0 с 3x-ui v3.1+ (у нас v3.4.2).

P2: модель py3xui `StreamSettings` требует поле `security`, но инбаунды без TLS/Reality
(например shadowsocks) отдают `streamSettings={"network":"tcp"}` без него → pydantic
ValidationError → `inbound.get_list()` падает ЦЕЛИКОМ, унося весь VPN-слой.

Делаем `security` опциональным (default "none") и пересобираем родительскую модель Inbound
(иначе кэш core-schema pydantic v2 игнорирует патч поля). Идемпотентно.

Проверено вживую на панели v3.4.2: после патча парсятся и vless+reality, и shadowsocks-инбаунды.
Техдолг: monkeypatch зависимости — при обновлении py3xui перепроверить (или перейти на вендор-форк).
"""
import logging

logger = logging.getLogger(__name__)

_applied = False


def apply_py3xui_patches() -> None:
    global _applied
    if _applied:
        return
    try:
        from py3xui.inbound.inbound import Inbound
        from py3xui.inbound.stream_settings import StreamSettings

        field = StreamSettings.model_fields.get("security")
        if field is not None and field.is_required():
            field.default = "none"
            field.default_factory = None
            StreamSettings.model_rebuild(force=True)
            Inbound.model_rebuild(force=True)
            logger.info("py3xui patch applied: StreamSettings.security is now optional.")
        _applied = True
    except Exception as exception:
        # Не валим старт из-за смены внутренней структуры py3xui — только предупреждаем.
        logger.error(f"Failed to apply py3xui compatibility patch: {exception}")
