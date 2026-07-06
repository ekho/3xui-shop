from dataclasses import dataclass, field
from typing import Any

from app.bot.utils.constants import DEFAULT_INBOUND_GROUPS, Currency


@dataclass
class Plan:
    devices: int
    prices: dict[str, dict[int, float]]
    traffic_gb: int = 0  # G2: лимит трафика в ГБ (0 = безлимит). Опционален для обратной совместимости.
    # Набор групп инбаундов тарифа (профиль = union групп). Старые записи без поля → дефолт.
    inbound_groups: list[str] = field(default_factory=lambda: list(DEFAULT_INBOUND_GROUPS))

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Plan":
        return cls(
            devices=data["devices"],
            traffic_gb=data.get("traffic_gb", 0),  # старые plans.json без поля → безлимит
            inbound_groups=list(data.get("inbound_groups") or DEFAULT_INBOUND_GROUPS),
            prices={k: {int(m): p for m, p in v.items()} for k, v in data["prices"].items()},
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            "devices": self.devices,
            "traffic_gb": self.traffic_gb,
            "inbound_groups": self.inbound_groups,
            "prices": {k: {str(m): p for m, p in v.items()} for k, v in self.prices.items()},
        }

    def get_price(self, currency: Currency | str, duration: int) -> float:
        if isinstance(currency, str):
            currency = Currency.from_code(currency)

        return self.prices[currency.code][duration]
