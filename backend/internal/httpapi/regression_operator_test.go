package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
)

func operatorActors(t *testing.T) (*regressionFixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e := fixture(t)
	customer := verified(t, s, e, "operator-customer@example.test")
	operator := verified(t, s, e, "operator-actor@example.test")
	if err := s.changeOperatorRole(context.Background(), operator, true); err != nil {
		t.Fatal("grant", err)
	}
	return s, customer, operator
}

func TestRegressionOperatorWebOnlyDecisionAndRole(t *testing.T) {
	s, customer, operator := operatorActors(t)
	ctx := context.Background()
	s.cfg.Accounts.Operators = nil
	panel := panelFixture(t, s)
	request, created, err := s.createTrialRequest(ctx, customer, uuid.New(), wire.TrialRequestInput{})
	if err != nil || !created {
		t.Fatal("web-only request", err)
	}
	key := uuid.New()
	out, created, err := s.decideOperatorTrial(ctx, operator, request.RequestId, key, wire.OperatorDecisionInput{Decision: "approve", Reason: ""})
	if err != nil || !created || out.OperationId == nil {
		t.Fatal("web-only approval", err)
	}
	again, created, err := s.decideOperatorTrial(ctx, operator, request.RequestId, key, wire.OperatorDecisionInput{Decision: "approve", Reason: ""})
	if err != nil || created || again.OperationId == nil || *again.OperationId != *out.OperationId {
		t.Fatal("lost response replay", err)
	}
	if err = s.provision(ctx, *out.OperationId); err != nil || panel.adds != 1 {
		t.Fatal("web-only worker", err)
	}
	if _, err = s.subscriptionKey(ctx, customer); err != nil {
		t.Fatal("web-only own key", err)
	}
	decision, err := testTrialRow(ctx, s.pool, request.RequestId)
	if err != nil || decision.OperatorAccountID == nil || *decision.OperatorAccountID != operator || decision.OperatorTgID.Valid || decision.DecisionSource != "operator" {
		t.Fatal("actor persisted", err)
	}
	var grants, jobs, audit int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=$1`, customer).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("one grant", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='trial_provision'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("one job", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='trial_approved' AND operator_account_id=$2`, customer, operator).Scan(&audit); err != nil || audit != 1 {
		t.Fatal("web audit", err)
	}
	if err = s.changeOperatorRole(ctx, operator, false); err != nil {
		t.Fatal("revoke", err)
	}
	if _, _, err = s.decideOperatorTrial(ctx, operator, request.RequestId, uuid.New(), wire.OperatorDecisionInput{Decision: "approve"}); status(err) != 403 {
		t.Fatal("revoked role", err)
	}
}

func TestRegressionOperatorActionsRejectNULBeforeDatabase(t *testing.T) {
	s, _, operator := operatorActors(t)
	ctx := context.Background()
	if _, _, err := s.decideOperatorTrial(ctx, operator, uuid.New(), uuid.New(), wire.OperatorDecisionInput{Decision: "reject", Reason: "bad\x00reason"}); status(err) != 400 {
		t.Fatal("decision reason NUL", err)
	}
	if _, _, err := s.reconsiderOperatorTrial(ctx, operator, uuid.New(), uuid.New(), "bad\x00reason"); status(err) != 400 {
		t.Fatal("reconsider reason NUL", err)
	}
	if _, _, err := s.reconcileOperatorTrial(ctx, operator, uuid.New(), uuid.New(), "bad\x00reason"); status(err) != 400 {
		t.Fatal("reconcile reason NUL", err)
	}
	if _, _, err := s.createTelegramTrial(ctx, operator, uuid.New(), wire.OperatorTelegramTrialInput{TelegramId: "123", DisplayName: "bad\x00name", Locale: "ru"}); status(err) != 400 {
		t.Fatal("Telegram name NUL", err)
	}
}

func TestRegressionOperatorTelegramOriginHasNoWebCredentials(t *testing.T) {
	s, _, operator := operatorActors(t)
	ctx := context.Background()
	s.cfg.Accounts.Operators = nil
	input := wire.OperatorTelegramTrialInput{TelegramId: "9223372036854775806", DisplayName: "Fixture Person", Locale: "ru"}
	key := uuid.New()
	out, created, err := s.createTelegramTrial(ctx, operator, key, input)
	if err != nil || !created || out.OperationId == uuid.Nil || out.Client.Email != nil || out.Client.TelegramId == nil || *out.Client.TelegramId != input.TelegramId {
		t.Fatal("Telegram-only trial", err)
	}
	again, created, err := s.createTelegramTrial(ctx, operator, key, input)
	if err != nil || created || again.OperationId != out.OperationId {
		t.Fatal("Telegram replay", err)
	}
	changed := input
	changed.DisplayName = "Different Person"
	if _, _, err = s.createTelegramTrial(ctx, operator, key, changed); status(err) != 409 {
		t.Fatal("Telegram idempotency payload mismatch", err)
	}
	if _, _, err = s.createTelegramTrial(ctx, operator, uuid.New(), input); status(err) != 409 {
		t.Fatal("duplicate Telegram identity", err)
	}
	s.cfg.Subscriptions.TrialEnabled = false
	if _, _, err = s.createTelegramTrial(ctx, operator, uuid.New(), wire.OperatorTelegramTrialInput{TelegramId: "9223372036854775805", DisplayName: "Another Person", Locale: "ru"}); status(err) != 403 {
		t.Fatal("disabled Telegram trial", err)
	}
	a, err := s.accountByID(ctx, out.Client.AccountId)
	if err != nil || a.Kind != "telegram" || a.EmailKey != nil || a.PasswordSet || a.VerifiedAt != nil || a.TermsVersion != nil || a.PrivacyVersion != nil || !accounts.SourceEligible(a) {
		t.Fatal("invented web credentials", err)
	}
	r, err := testTrialRow(ctx, s.pool, out.Request.RequestId)
	if err != nil || r.OperatorAccountID == nil || *r.OperatorAccountID != operator || r.OperatorTgID.Valid || r.DecisionSource != "operator" {
		t.Fatal("trial actor", err)
	}
	raw := opaque()
	now := s.now()
	if _, err = s.pool.Exec(ctx, `INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at) VALUES($1,$2,$3,$4,$4,$5)`, digest(raw), a.ID, opaque(), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.authenticate(ctx, raw); status(err) != 401 {
		t.Fatal("Telegram-only account entered web session", err)
	}
	if _, err = s.getSessionContext(ctx, raw); status(err) != 401 {
		t.Fatal("Telegram-only account reached session context", err)
	}
}

func TestRegressionOperatorTelegramProvisionAndUncertainty(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "uncertain"}[uncertain], func(t *testing.T) {
			s, _, operator := operatorActors(t)
			p := panelFixture(t, s)
			p.dropAdd = uncertain
			out, created, err := s.createTelegramTrial(context.Background(), operator, uuid.New(), wire.OperatorTelegramTrialInput{TelegramId: "9223372036854775804", DisplayName: "Fixture Person", Locale: "ru"})
			if err != nil || !created {
				t.Fatal("Telegram trial", err)
			}
			if err = s.provision(context.Background(), out.OperationId); err != nil {
				t.Fatal("Telegram provisioning", err)
			}
			var state string
			if err = s.pool.QueryRow(context.Background(), `SELECT status FROM trial_operations WHERE id=$1`, out.OperationId).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if uncertain {
				if state != "needs_review" || p.adds != 1 {
					t.Fatal("uncertain panel write must preserve one identity for review")
				}
				if _, _, err = s.createTelegramTrial(context.Background(), operator, uuid.New(), wire.OperatorTelegramTrialInput{TelegramId: "9223372036854775804", DisplayName: "Fixture Person", Locale: "ru"}); status(err) != 409 {
					t.Fatal("uncertain trial reissued", err)
				}
				return
			}
			if state != "applied" || p.adds != 1 {
				t.Fatal("Telegram-only worker did not apply one client")
			}
			if _, err = s.subscriptionKey(context.Background(), out.Client.AccountId); err != nil {
				t.Fatal("Telegram-only client own key", err)
			}
		})
	}
}

func TestRegressionOperatorConcurrentBotWebDecisionOneGrant(t *testing.T) {
	s, customer, operator := operatorActors(t)
	ctx := context.Background()
	request, _, err := s.createTrialRequest(ctx, customer, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, e := s.decideTrialRequest(ctx, request.RequestId, wire.DecisionInput{OperatorTgId: 101, Decision: "approve", CallbackQueryId: uuid.NewString()})
		results <- e
	}()
	go func() {
		defer wg.Done()
		_, _, e := s.decideOperatorTrial(ctx, operator, request.RequestId, uuid.New(), wire.OperatorDecisionInput{Decision: "approve"})
		results <- e
	}()
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("concurrent decision", err)
		}
	}
	var grants, ops, jobs int
	for _, item := range []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM trial_grants WHERE account_id=$1`, &grants},
		{`SELECT count(*) FROM trial_operations WHERE account_id=$1`, &ops},
	} {
		if err = s.pool.QueryRow(ctx, item.query, customer).Scan(item.dest); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='trial_provision'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || ops != 1 || jobs != 1 {
		t.Fatal("duplicate grant operation or job")
	}
}

func TestRegressionOperatorSearchHistoryAndKeyDenial(t *testing.T) {
	s, customer, operator := operatorActors(t)
	ctx := context.Background()
	search, err := s.searchOperatorClients(ctx, operator, wire.OperatorSearchInput{Q: "operator-customer", Page: 1, PerPage: 50})
	if err != nil || search.Total != 1 || len(search.Clients) != 1 || search.Clients[0].AccountId != customer {
		t.Fatal("search", err)
	}
	if _, err = s.searchOperatorClients(ctx, customer, wire.OperatorSearchInput{Page: 1, PerPage: 50}); status(err) != 403 {
		t.Fatal("client searched operator list", err)
	}
	card, err := s.operatorClient(ctx, operator, customer)
	if err != nil || card.Client.AccountId != customer || card.Client.Email == nil || card.Server != nil || card.Support != nil {
		t.Fatal("card", err)
	}
	if _, _, err = s.createTrialRequest(ctx, customer, uuid.New(), wire.TrialRequestInput{}); err != nil {
		t.Fatal(err)
	}
	history, err := s.operatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "audit"})
	if err != nil || len(history.AuditEvents) == 0 {
		t.Fatal("audit history", err)
	}
	if _, err = s.operatorClientKey(ctx, customer, customer); status(err) != 403 {
		t.Fatal("client fetched operator key", err)
	}
	if _, err = s.operatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "audit", BeforeCreatedAt: ptr(time.Now())}); status(err) != 400 {
		t.Fatal("partial cursor", err)
	}
}

func TestRegressionOperatorCardReadsRestrictedTarget(t *testing.T) {
	s, customer, operator := operatorActors(t)
	if _, err := s.pool.Exec(context.Background(), `UPDATE accounts SET restricted=true WHERE id=$1`, customer); err != nil {
		t.Fatal(err)
	}
	card, err := s.operatorClient(context.Background(), operator, customer)
	if err != nil || !card.Client.Restricted || card.Client.AccountId != customer {
		t.Fatal("restricted target card", err)
	}
	if _, err = s.operatorClientKey(context.Background(), operator, customer); status(err) != 403 {
		t.Fatal("restricted key", err)
	}
}

func TestRegressionOperatorHistoryStablePages(t *testing.T) {
	s, customer, operator := operatorActors(t)
	ctx := context.Background()
	when := s.now()
	for i := 0; i < 52; i++ {
		if _, err := s.pool.Exec(ctx, `INSERT INTO audit_events(id,created_at,action,account_id) VALUES($1,$2,'fixture',$3)`, uuid.New(), when, customer); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id)
			VALUES($1,$2,'rejected','',$3,$3,101)`, uuid.New(), customer, when); err != nil {
			t.Fatal(err)
		}
	}
	card, err := s.operatorClient(ctx, operator, customer)
	if err != nil || !card.AuditHasMore || !card.TrialHasMore || len(card.AuditEvents) != 50 || len(card.TrialRequests) != 50 {
		t.Fatal("first history pages", err)
	}
	lastAudit := card.AuditEvents[49]
	audit, err := s.operatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "audit", BeforeCreatedAt: &lastAudit.CreatedAt, BeforeId: &lastAudit.Id})
	if err != nil || audit.HasMore || len(audit.AuditEvents) != 2 {
		t.Fatal("older audit page", err)
	}
	lastTrial := card.TrialRequests[49]
	trials, err := s.operatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "trials", BeforeCreatedAt: &lastTrial.CreatedAt, BeforeId: &lastTrial.RequestId})
	if err != nil || trials.HasMore || len(trials.TrialRequests) != 2 {
		t.Fatal("older trial page", err)
	}
	for _, item := range audit.AuditEvents {
		for _, first := range card.AuditEvents {
			if item.Id == first.Id {
				t.Fatal("duplicate audit cursor")
			}
		}
	}
	for _, item := range trials.TrialRequests {
		for _, first := range card.TrialRequests {
			if item.RequestId == first.RequestId {
				t.Fatal("duplicate trial cursor")
			}
		}
	}
}

func TestRegressionOperatorRevocationAndRestrictionWinQueuedDecision(t *testing.T) {
	for _, change := range []string{"revoke", "restrict"} {
		t.Run(change, func(t *testing.T) {
			s, customer, operator := operatorActors(t)
			ctx := context.Background()
			request, _, err := s.createTrialRequest(ctx, customer, uuid.New(), wire.TrialRequestInput{})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, operator); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, _, writeErr := s.decideOperatorTrial(ctx, operator, request.RequestId, uuid.New(), wire.OperatorDecisionInput{Decision: "approve"})
				done <- writeErr
			}()
			waitSupportAccountLock(t, s)
			if change == "revoke" {
				_, err = tx.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator)
			} else {
				_, err = tx.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, operator)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; status(err) != 403 {
				t.Fatal("stale preflight authorized decision", err)
			}
			var grants int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=$1`, customer).Scan(&grants); err != nil || grants != 0 {
				t.Fatal("unauthorized grant", err)
			}
			if change == "restrict" {
				if err = s.changeOperatorRole(ctx, operator, false); err != nil {
					t.Fatal("restricted revoke", err)
				}
				if err = s.changeOperatorRole(ctx, operator, true); status(err) != 403 {
					t.Fatal("restricted grant", err)
				}
			}
		})
	}
}
