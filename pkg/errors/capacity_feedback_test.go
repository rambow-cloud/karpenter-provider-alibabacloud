package errors

import (
	"context"
	"fmt"
	"testing"

	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestCapacityFeedbackOnlyTrustsStructuredStockCodes(t *testing.T) {
	for _, code := range []string{ErrCodeNoStock, ErrCodeZoneNotOnSale, ErrCodeResourceTypeNotSupported, ErrCodeVSwitchIPNotEnough, ErrCodeForbiddenRAM, ErrCodeInvalidParameter, ErrCodeThrottlingUser, ErrCodeServiceUnavailable} {
		want := code == ErrCodeNoStock || code == ErrCodeZoneNotOnSale || code == ErrCodeResourceTypeNotSupported
		for _, sdkErr := range []error{tea.NewSDKError(map[string]interface{}{"code": code, "message": "offline"}), dara.NewSDKError(map[string]interface{}{"code": code, "message": "offline"})} {
			require.Equal(t, want, IsInstanceTypeCapacityError(fmt.Errorf("launch failed: %w", sdkErr)), code)
		}
	}
	require.False(t, IsInstanceTypeCapacityError(nil))
	require.False(t, IsInstanceTypeCapacityError(context.DeadlineExceeded))
	require.False(t, IsInstanceTypeCapacityError(fmt.Errorf("previous error was %s", ErrCodeNoStock)))
	require.False(t, IsInstanceTypeCapacityError(tea.NewSDKError(map[string]interface{}{"code": ErrCodeForbiddenRAM, "message": ErrCodeNoStock})))
}
