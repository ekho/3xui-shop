package app

import "testing"

func TestHeleketConfig(t *testing.T)           { testCryptoConfig(t, "Heleket") }
func TestHeleketConfigValidation(t *testing.T) { testCryptoConfigValidation(t, "Heleket") }
