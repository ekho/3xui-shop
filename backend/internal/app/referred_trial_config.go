package app

import (
	"errors"
	"math"
	"os"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/bonuses"
)

func readReferredTrialConfig() (bonuses.ReferredTrialConfig, error) {
	c := bonuses.ReferredTrialConfig{PeriodDays: 7}
	if value := os.Getenv("SHOP_REFERRED_TRIAL_ENABLED"); value != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid SHOP_REFERRED_TRIAL_ENABLED")
		}
	}
	if value := os.Getenv("SHOP_REFERRED_TRIAL_PERIOD"); value != "" {
		var err error
		c.PeriodDays, err = strconv.ParseInt(value, 10, 64)
		if err != nil || c.PeriodDays <= 0 || c.PeriodDays > math.MaxInt64/int64(24*time.Hour) {
			return c, errors.New("invalid SHOP_REFERRED_TRIAL_PERIOD")
		}
	}
	return c, nil
}
