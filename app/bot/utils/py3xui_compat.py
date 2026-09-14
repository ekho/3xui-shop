"""Совместимость py3xui 0.7.0 с 3x-ui v3.1+.

P2: модель py3xui не принимает два валидных ответа панели: `StreamSettings` без
`security` и `streamSettings=null`. Любой такой инбаунд вызывает ValidationError,
из-за которого `inbound.get_list()` падает целиком, унося весь VPN-слой.

Делаем `security` опциональным (default "none"), разрешаем null и пересобираем
родительскую модель Inbound (иначе кэш core-schema pydantic v2 игнорирует патч). Идемпотентно.

Проверено вживую на панелях v3.4.2 и v3.7.0.
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

        security = StreamSettings.model_fields.get("security")
        if security is not None and security.is_required():
            security.default = "none"
            security.default_factory = None
            StreamSettings.model_rebuild(force=True)
        stream_settings = Inbound.model_fields.get("stream_settings")
        if stream_settings is not None:
            stream_settings.annotation = StreamSettings | str | None
        Inbound.model_rebuild(force=True)
        logger.info("py3xui patch applied: StreamSettings is now optional and nullable.")
        _applied = True
    except Exception as exception:
        # Не валим старт из-за смены внутренней структуры py3xui — только предупреждаем.
        logger.error(f"Failed to apply py3xui compatibility patch: {exception}")
