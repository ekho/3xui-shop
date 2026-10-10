package app

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"os"
	"strconv"
)

func readRewardConfig() (bonuses.RewardConfig, error) {
	c := bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}
	if value := os.Getenv("SHOP_REFERRER_REWARD_ENABLED"); value != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid SHOP_REFERRER_REWARD_ENABLED")
		}
	}
	// Preserve the legacy setting name and its disabled money mode.
	switch os.Getenv("SHOP_REFERRED_REWARD_TYPE") {
	case "", "days":
	case "money":
		c.Enabled = false
	default:
		return c, errors.New("invalid SHOP_REFERRED_REWARD_TYPE")
	}
	for name, dest := range map[string]*int{"SHOP_REFERRER_LEVEL_ONE_PERIOD": &c.LevelOneDays, "SHOP_REFERRER_LEVEL_TWO_PERIOD": &c.LevelTwoDays} {
		if value := os.Getenv(name); value != "" {
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 || v > 365 {
				return c, errors.New("invalid " + name)
			}
			*dest = v
		}
	}
	return c, nil
}
