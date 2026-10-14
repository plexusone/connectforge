package cryptotest_test

import (
	"testing"

	"github.com/plexusone/connectforge/cryptotest"
)

func TestConformance(t *testing.T) { cryptotest.Run(t, cryptotest.New()) }
