import hashlib
import secrets
import string
import uuid
from datetime import datetime

CHARSET = string.ascii_uppercase + string.digits

# Алфавит subId как в 3x-ui: RandomUtil.getSeq({hasUppercase:false}) = цифры + строчные
# латинские (frontend/src/utils/index.ts, v3.4.2). Порядок неважен — выбор равновероятный.
SUB_ID_CHARSET = string.digits + string.ascii_lowercase
SUB_ID_LENGTH = 16


def split_text(text: str, chunk_size: int = 4096) -> list[str]:
    """Split text into chunks of a given size."""
    return [text[i : i + chunk_size] for i in range(0, len(text), chunk_size)]


def generate_sub_id(length: int = SUB_ID_LENGTH) -> str:
    """Сгенерировать subId в формате 3x-ui — RandomUtil.randomLowerAndNum(16).

    Панель берёт `length` символов из алфавита [0-9a-z] криптостойким ГСЧ. Здесь тот же
    алфавит и длина через secrets.choice — в отличие от `v % 36` панели, выборка без
    modulo-смещения (строже, формат идентичен). Это ключ страницы подписки (subId), а
    НЕ креденшл клиента (id остаётся UUID).
    """
    return "".join(secrets.choice(SUB_ID_CHARSET) for _ in range(length))


def generate_code(length: int = 8) -> str:
    """Generate an 8-character alphanumeric promocode."""
    return "".join(secrets.choice(CHARSET) for _ in range(length))


def generate_hash(text: str, length: int = 8) -> str:
    """
    Generate a hash from text, using timestamp for uniqueness.
    Always includes at least one letter to distinguish from numeric IDs.
    """
    timestamp = datetime.utcnow().timestamp()
    combined = f"{text}_{timestamp}"
    full_hash = hashlib.md5(combined.encode()).hexdigest()

    result = full_hash[: length - 1]

    result += secrets.choice(string.ascii_lowercase)

    return result
