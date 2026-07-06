from enum import Enum


class NavMain(str, Enum):
    START = "start"
    MAIN_MENU = "main_menu"
    CLOSE_NOTIFICATION = "close_notification"
    REDIRECT_TO_DOWNLOAD = "redirect_to_download"


class NavProfile(str, Enum):
    MAIN = "profile"
    SHOW_KEY = "show_key"
    CANCEL_STARS_SUB = "cancel_stars_sub"  # G4: отменить автопродление Stars
    RESUME_STARS_SUB = "resume_stars_sub"  # m4: возобновить (пока период активен)


class NavReferral(str, Enum):
    MAIN = "referral"
    GET_REFERRED_TRIAL = "get_referral_trial"


class NavSupport(str, Enum):
    MAIN = "support"
    HOW_TO_CONNECT = "how_to_connect"
    VPN_NOT_WORKING = "vpn_not_working"


class NavDownload(str, Enum):
    MAIN = "download"
    PLATFORM = "platform"
    PLATFORM_IOS = f"{PLATFORM}_ios"
    PLATFORM_ANDROID = f"{PLATFORM}_android"
    PLATFORM_WINDOWS = f"{PLATFORM}_windows"
    SHOW_QR = "download_show_qr"


class NavSubscription(str, Enum):
    MAIN = "subscription"
    CHANGE = "change"
    EXTEND = "extend"
    PROCESS = "process"
    DEVICES = "devices"
    DURATION = "duration"
    PROMOCODE = "promocode"
    GET_TRIAL = "get_trial"

    PAY = "pay"
    PAY_YOOKASSA = f"{PAY}_yookassa"
    PAY_TELEGRAM_STARS = f"{PAY}_telegram_stars"
    PAY_CRYPTOMUS = f"{PAY}_cryptomus"
    PAY_HELEKET = f"{PAY}_heleket"
    PAY_YOOMONEY = f"{PAY}_yoomoney"
    PAY_MANUAL = f"{PAY}_manual"  # G3: ручная карта→карта
    BACK_TO_DURATION = "back_to_duration"
    BACK_TO_PAYMENT = "back_to_payment"


class NavAdminTools(str, Enum):
    MAIN = "admin_tools"
    TEST = "test"
    SERVER_MANAGEMENT = "server_management"
    SHOW_SERVER = "show_server"
    PING_SERVER = "ping_server"
    ADD_SERVER = "add_server"
    ADD_SERVER_BACK = "add_server_back"
    СONFIRM_ADD_SERVER = "сonfirm_add_server"
    DELETE_SERVER = "delete_server"
    EDIT_SERVER = "edit_server"
    SYNC_SERVERS = "sync_servers"
    STATISTICS = "statistics"
    USER_EDITOR = "user_editor"

    REJECTED_USERS = "rejected_users"
    SHOW_REJECTED_PAGE = "show_rejected_page"
    SHOW_REJECTED_DETAILS = "show_rejected_details"
    CONFIRM_UNREJECT_USER = "confirm_unreject_user"
    UNREJECT_USER = "unreject_user"

    INVITE_EDITOR = "invite_editor"
    CREATE_INVITE = "create_invite"
    DELETE_INVITE = "delete_invite"
    LIST_INVITES = "list_invites"
    SHOW_INVITE_PAGE = "show_invite_page"
    SHOW_INVITE_DETAILS = "show_invite_details"
    TOGGLE_INVITE_STATUS = "toggle_invite_status"
    CONFIRM_DELETE_INVITE = "confirm_delete_invite"

    PROMOCODE_EDITOR = "promocode_editor"
    CREATE_PROMOCODE = "create_promocode"
    DELETE_PROMOCODE = "delete_promocode"
    EDIT_PROMOCODE = "edit_promocode"

    PLAN_EDITOR = "plan_editor"
    CREATE_PLAN = "create_plan"
    CONFIRM_CREATE_PLAN = "confirm_create_plan"
    SHOW_PLAN = "show_plan"
    EDIT_PLAN_TRAFFIC = "edit_plan_traffic"
    EDIT_PLAN_PRICES = "edit_plan_prices"
    EDIT_PLAN_GROUPS = "edit_plan_groups"
    CONFIRM_DELETE_PLAN = "confirm_delete_plan"
    DELETE_PLAN = "delete_plan"

    GROUP_MANAGEMENT = "group_mgmt"
    USER_GROUPS = "user_groups"
    TOGGLE_USER_GROUP = "tgl_usr_grp"

    NOTIFICATION = "notification"
    SEND_NOTIFICATION_USER = "send_notification_user"
    SEND_NOTIFICATION_ALL = "send_notification_all"
    CONFIRM_SEND_NOTIFICATION = "confirm_send_notification"
    LAST_NOTIFICATION = "last_notification"
    EDIT_NOTIFICATION = "edit_notification"
    DELETE_NOTIFICATION = "delete_notification"

    CREATE_BACKUP = "create_backup"

    MAINTENANCE_MODE = "maintenance_mode"
    MAINTENANCE_MODE_ENABLE = "maintenance_mode_enable"
    MAINTENANCE_MODE_DISABLE = "maintenance_mode_disable"

    RESTART_BOT = "restart_bot"
