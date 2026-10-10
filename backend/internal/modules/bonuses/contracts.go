package bonuses

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }
