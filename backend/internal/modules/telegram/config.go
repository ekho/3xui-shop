package telegram

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Enabled   bool
	Token     string
	Operators []int64
}

var tokenPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}:[A-Za-z0-9_-]{20,128}$`)

func (c Config) validate() error {
	if !c.Enabled {
		return nil
	}
	if !tokenPattern.MatchString(c.Token) || len(c.Operators) == 0 {
		return errors.New("invalid Telegram configuration")
	}
	for _, id := range c.Operators {
		if id <= 0 {
			return errors.New("invalid Telegram operators")
		}
	}
	return nil
}
func LoadConfig(operators []int64) (Config, error) {
	c := Config{Operators: append([]int64(nil), operators...)}
	if value := os.Getenv("TELEGRAM_ENABLED"); value != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid TELEGRAM_ENABLED")
		}
	}
	if !c.Enabled {
		return c, nil
	}
	p := os.Getenv("BOT_TOKEN_FILE")
	if p == "" || os.Getenv("BOT_TOKEN") != "" {
		return Config{}, errors.New("BOT_TOKEN_FILE required without plaintext input")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Config{}, errors.New("unreadable BOT_TOKEN_FILE")
	}
	c.Token = strings.TrimSpace(string(b))
	return c, c.validate()
}
