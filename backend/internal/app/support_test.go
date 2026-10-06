package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// New neutral DTOs must still read the JSON and hash committed by the old owner.
func TestSupportPersistedCompatibility(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := NewService(e.Pool, e.Redis, nil, platform.Config{RateNamespace: uuid.NewString()})
	customer, conversation, message, key := paymentAccount(t, e), uuid.New(), uuid.New(), uuid.New()
	when := e.Clock()
	file := []byte("AB")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO support_conversations(id,account_id,status,support_banned,created_at,updated_at)
		VALUES($1,$2,'closed',true,$3,$3)`, conversation, customer, when); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if err := e.Pool.QueryRow(ctx, `INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes)
		VALUES($1,$2,$3,'customer','old message',$4,'proof.txt',$5) RETURNING sequence`, message, conversation, customer, when, file).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	old := wire.SupportMessage{Attachment: &wire.SupportAttachment{Name: "proof.txt", SizeBytes: 2}, CreatedAt: when, Delivery: "stored", Id: message, Sender: "customer", Sequence: sequence, Text: "old message"}
	current := support.SupportMessage{Attachment: &support.SupportAttachment{Name: "proof.txt", SizeBytes: 2}, CreatedAt: when, Delivery: "stored", Id: message, Sender: "customer", Sequence: sequence, Text: "old message"}
	oldConversation := wire.SupportConversation{CreatedAt: when, CustomerReceivedSequence: 1, Id: conversation, OperatorReceivedSequence: 2, Status: "closed", SupportBanned: true, UpdatedAt: when}
	newConversation := support.SupportConversation{CreatedAt: when, CustomerReceivedSequence: 1, Id: conversation, OperatorReceivedSequence: 2, Status: "closed", SupportBanned: true, UpdatedAt: when}
	for _, pair := range []struct{ old, current any }{
		{oldConversation, newConversation}, {old, current},
		{wire.SupportMessage{Id: message}, support.SupportMessage{Id: message}},
		{wire.SupportResult{}, support.SupportResult{}},
		{wire.SupportResult{Messages: []wire.SupportMessage{}}, support.SupportResult{Messages: []support.SupportMessage{}}},
		{wire.SupportResult{Conversation: &oldConversation, Messages: []wire.SupportMessage{old}, OldestSequence: &sequence}, support.SupportResult{Conversation: &newConversation, Messages: []support.SupportMessage{current}, OldestSequence: &sequence}},
	} {
		before, err := json.Marshal(pair.old)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(pair.current)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("persisted support JSON changed", err)
		}
	}
	oldInput, _ := json.Marshal(struct {
		Target     uuid.UUID
		Operator   bool
		Text, Name string
		FileHash   [32]byte
		FileSize   int
	}{customer, false, "old message", "proof.txt", sha256.Sum256(file), len(file)})
	hash := sha256.Sum256(oldInput)
	result, _ := json.Marshal(old)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at)
		VALUES($1,'createSupportMessage',$2,$3,$4,$5)`, "account:"+customer.String(), key, hash[:], result, when); err != nil {
		t.Fatal(err)
	}
	got, created, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "old message", "proof.txt", file)
	gotJSON, marshalErr := json.Marshal(got)
	if err != nil || marshalErr != nil || created || !bytes.Equal(gotJSON, result) {
		t.Fatal("old result did not replay after support ban", err, marshalErr)
	}
	if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, key, "changed", "proof.txt", file); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed old payload accepted", err)
	}
	var messages, audits int
	var unchanged bool
	if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages),
		(SELECT count(*) FROM audit_events WHERE action LIKE 'support_%'),status='closed' AND support_banned AND updated_at=$2
		FROM support_conversations WHERE id=$1`, conversation, when).Scan(&messages, &audits, &unchanged); err != nil || messages != 1 || audits != 0 || !unchanged {
		t.Fatal("old replay changed stored effects", err)
	}
}

// Real app composition must preserve protected reads and transaction rollback.
func TestSupportComposition(t *testing.T) {
	for _, rejectAudit := range []bool{false, true} {
		name := "flow"
		if rejectAudit {
			name = "audit rollback"
		}
		t.Run(name, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			s := NewService(e.Pool, e.Redis, nil, platform.Config{RateNamespace: uuid.NewString()})
			customer, operator := paymentAccount(t, e), paymentAccount(t, e)
			if err := s.ChangeOperatorRole(ctx, operator, true); err != nil {
				t.Fatal(err)
			}
			card, err := s.OperatorClient(ctx, operator, customer)
			if err != nil || card.Support != nil {
				t.Fatal("missing conversation must remain nil", err)
			}
			if rejectAudit {
				if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION reject_support_audit() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN IF NEW.action='support_message' THEN RAISE EXCEPTION 'controlled audit rejection'; END IF; RETURN NEW; END $$;
					CREATE TRIGGER reject_support BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_support_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			message, created, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "hello", "proof.txt", []byte("private bytes"))
			if rejectAudit {
				if err == nil || err.Error() != "SERVICE_UNAVAILABLE" || created {
					t.Fatal("audit failure must reject the message", err)
				}
				var conversations, messages, records, audits int
				if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_conversations),
					(SELECT count(*) FROM support_messages),(SELECT count(*) FROM idempotency_records),
					(SELECT count(*) FROM audit_events WHERE action='support_message')`).Scan(&conversations, &messages, &records, &audits); err != nil || conversations != 0 || messages != 0 || records != 0 || audits != 0 {
					t.Fatal("partial support commit on audit failure", err)
				}
				return
			}
			if err != nil || !created || message.Delivery != "stored" || message.Attachment == nil {
				t.Fatal("composed message", err)
			}
			card, err = s.OperatorClient(ctx, operator, customer)
			if err != nil || card.Support == nil || card.Support.Status != "open" {
				t.Fatal("composed operator conversation", err)
			}
			name, body, err := s.SupportAttachment(ctx, operator, message.Id)
			if err != nil || name != "proof.txt" || !bytes.Equal(body, []byte("private bytes")) {
				t.Fatal("composed private attachment", err)
			}
			if err = s.AcknowledgeSupport(ctx, operator, customer, true, message.Sequence); err != nil {
				t.Fatal(err)
			}
			view, err := s.Support(ctx, customer, customer, false)
			if err != nil || len(view.Messages) != 1 || view.Messages[0].Delivery != "delivered" {
				t.Fatal("composed recipient ack", err)
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", customer); err != nil {
				t.Fatal(err)
			}
			if err = s.SetSupportBan(ctx, operator, customer, true, "abuse"); err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "blocked", "", nil); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
				t.Fatal("support ban not composed", err)
			}
			var vpnBanned bool
			if err = e.Pool.QueryRow(ctx, "SELECT vpn_banned FROM accounts WHERE id=$1", customer).Scan(&vpnBanned); err != nil || !vpnBanned {
				t.Fatal("support ban changed VPN", err)
			}
			if err = s.ChangeOperatorRole(ctx, operator, false); err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "revoked", "", nil); err == nil || err.Error() != "INVALID_CREDENTIALS" {
				t.Fatal("revoked operator wrote support message", err)
			}
		})
	}
}
