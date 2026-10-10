package app

import (
	"strings"
	"testing"
)

func TestRewardConfigDefaultsDisableMoneyAndBounds(t *testing.T) {
	for _, key := range []string{"SHOP_REFERRER_REWARD_ENABLED", "SHOP_REFERRED_REWARD_TYPE", "SHOP_REFERRER_LEVEL_ONE_PERIOD", "SHOP_REFERRER_LEVEL_TWO_PERIOD"} {
		t.Setenv(key, "")
	}
	c, err := readRewardConfig()
	if err != nil || !c.Enabled || c.LevelOneDays != 10 || c.LevelTwoDays != 3 {
		t.Fatal("legacy day defaults changed", err)
	}
	t.Setenv("SHOP_REFERRED_REWARD_TYPE", "money")
	c, err = readRewardConfig()
	if err != nil || c.Enabled {
		t.Fatal("money mode became a day grant", err)
	}
	t.Setenv("SHOP_REFERRED_REWARD_TYPE", "days")
	t.Setenv("SHOP_REFERRER_REWARD_ENABLED", "false")
	t.Setenv("SHOP_REFERRER_LEVEL_ONE_PERIOD", "0")
	t.Setenv("SHOP_REFERRER_LEVEL_TWO_PERIOD", "365")
	c, err = readRewardConfig()
	if err != nil || c.Enabled || c.LevelOneDays != 0 || c.LevelTwoDays != 365 {
		t.Fatal("disable/boundary snapshot changed", err)
	}
	for _, key := range []string{"SHOP_REFERRER_LEVEL_ONE_PERIOD", "SHOP_REFERRER_LEVEL_TWO_PERIOD"} {
		for _, value := range []string{"-1", "366", "1.5", "invalid-private-input"} {
			t.Setenv(key, value)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), value) {
				t.Fatal("invalid day setting reached dependencies or leaked value", err)
			}
		}
		t.Setenv(key, "")
	}
	for _, key := range []string{"SHOP_REFERRER_REWARD_ENABLED", "SHOP_REFERRED_REWARD_TYPE"} {
		t.Setenv(key, "invalid-private-input")
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "invalid-private-input") {
			t.Fatal("invalid switch reached dependencies or leaked value", err)
		}
		t.Setenv(key, "")
	}
}
