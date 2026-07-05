import re
from urllib.parse import urlparse

from app.bot.utils.constants import Currency

IP_PATTERN = re.compile(
    r"^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}" r"(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$"
)

PLAN_PRICE_CURRENCIES = [Currency.RUB, Currency.USD, Currency.XTR]


def is_valid_host(data: str) -> bool:
    parsed = urlparse(data)
    if all([parsed.scheme, parsed.netloc]):
        return True
    return bool(IP_PATTERN.match(data))


def is_valid_client_count(data: str) -> bool:
    return data.isdigit() and 1 <= int(data) <= 10000


def is_valid_user_id(data: str) -> bool:
    return data.isdigit() and 1 <= int(data) <= 1000000000000


def is_valid_message_text(data: str) -> bool:
    return len(data) <= 4096


def is_valid_traffic_gb(data: str) -> bool:
    return data.isdigit() and 0 <= int(data) <= 100000


def parse_plan_prices(text: str, durations: list[int]) -> dict[str, dict[str, float]] | None:
    """Разбирает построчный ввод "<срок> <RUB> <USD> <XTR>" в prices-словарь плана
    ({"RUB": {"30": 70.0, ...}, "USD": {...}, "XTR": {...}}). Требует ровно один набор цен
    на КАЖДЫЙ существующий срок (иначе payment_method_keyboard упадёт KeyError на пропущенной
    паре срок/валюта) — при несовпадении множества сроков или некорректном формате строки
    возвращает None.
    """
    lines = [line.strip() for line in text.strip().splitlines() if line.strip()]
    if not lines:
        return None

    parsed: dict[int, tuple[float, ...]] = {}
    for line in lines:
        parts = line.replace(",", ".").split()
        if len(parts) != 1 + len(PLAN_PRICE_CURRENCIES):
            return None
        try:
            duration = int(parts[0])
            values = tuple(float(part) for part in parts[1:])
        except ValueError:
            return None
        if duration in parsed or duration <= 0 or any(value < 0 for value in values):
            return None
        parsed[duration] = values

    if set(parsed) != set(durations):
        return None

    return {
        currency.code: {str(duration): values[index] for duration, values in parsed.items()}
        for index, currency in enumerate(PLAN_PRICE_CURRENCIES)
    }
