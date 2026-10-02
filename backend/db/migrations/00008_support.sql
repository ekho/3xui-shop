-- +goose Up
CREATE TABLE operator_accounts (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 granted_at timestamptz NOT NULL
);
CREATE TABLE support_conversations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL UNIQUE REFERENCES accounts(id),
 status text NOT NULL CHECK (status IN ('open','closed')),
 support_banned boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 customer_received_sequence bigint NOT NULL DEFAULT 0 CHECK (customer_received_sequence >= 0),
 operator_received_sequence bigint NOT NULL DEFAULT 0 CHECK (operator_received_sequence >= 0)
);
CREATE TABLE support_messages (
 id uuid PRIMARY KEY,
 conversation_id uuid NOT NULL REFERENCES support_conversations(id),
 sequence bigint GENERATED ALWAYS AS IDENTITY NOT NULL UNIQUE,
 sender_account_id uuid NOT NULL REFERENCES accounts(id),
 sender_kind text NOT NULL CHECK (sender_kind IN ('customer','operator')),
 text text NOT NULL CHECK (char_length(text) <= 4000),
 created_at timestamptz NOT NULL,
 attachment_name text CHECK (char_length(attachment_name) BETWEEN 1 AND 128),
 attachment_bytes bytea CHECK (octet_length(attachment_bytes) BETWEEN 1 AND 10485760),
 CHECK ((attachment_name IS NULL) = (attachment_bytes IS NULL)),
 CHECK (text <> '' OR attachment_bytes IS NOT NULL)
);
CREATE INDEX support_message_page ON support_messages(conversation_id,sequence DESC);
ALTER TABLE audit_events
 ADD COLUMN operator_account_id uuid REFERENCES accounts(id),
 ADD COLUMN support_message_id uuid REFERENCES support_messages(id),
 ADD CONSTRAINT audit_single_operator CHECK (operator_tg_id IS NULL OR operator_account_id IS NULL);

-- +goose Down
ALTER TABLE audit_events DROP CONSTRAINT audit_single_operator, DROP COLUMN operator_account_id, DROP COLUMN support_message_id;
DROP TABLE support_messages,support_conversations,operator_accounts;
