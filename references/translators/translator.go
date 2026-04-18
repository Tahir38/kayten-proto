// kayten-proto — references/translators/translator.go
package translators

import (
	hsmkeysv1 "github.com/kayten-gmbh/kayten-server/pkg/generated/kayten/hsm_keys/v1"
)

func ProvisioningStateFromWire(w byte) hsmkeysv1.ProvisioningState {
	switch w {
	case 0x00:
		return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONING_REQUIRED
	case 0x01:
		return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONED
	case 0x02:
		return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_LOCKED_WIPED
	default:
		return hsmkeysv1.ProvisioningState_PROVISIONING_STATE_UNSPECIFIED
	}
}

func ProvisioningStateToWire(s hsmkeysv1.ProvisioningState) byte {
	switch s {
	case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONING_REQUIRED:
		return 0x00
	case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_PROVISIONED:
		return 0x01
	case hsmkeysv1.ProvisioningState_PROVISIONING_STATE_LOCKED_WIPED:
		return 0x02
	default:
		return 0xFF
	}
}

func WipeReasonFromWire(w byte) hsmkeysv1.WipeReason {
	switch w {
	case 0x00:
		return hsmkeysv1.WipeReason_WIPE_REASON_NONE
	case 0x01:
		return hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT
	case 0x02:
		return hsmkeysv1.WipeReason_WIPE_REASON_USER_REQUEST
	case 0x03:
		return hsmkeysv1.WipeReason_WIPE_REASON_ATTESTATION_FAILED
	case 0x04:
		return hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT_RECOVERY
	default:
		return hsmkeysv1.WipeReason_WIPE_REASON_UNSPECIFIED
	}
}

func WipeReasonToWire(r hsmkeysv1.WipeReason) byte {
	switch r {
	case hsmkeysv1.WipeReason_WIPE_REASON_NONE:
		return 0x00
	case hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT:
		return 0x01
	case hsmkeysv1.WipeReason_WIPE_REASON_USER_REQUEST:
		return 0x02
	case hsmkeysv1.WipeReason_WIPE_REASON_ATTESTATION_FAILED:
		return 0x03
	case hsmkeysv1.WipeReason_WIPE_REASON_PIN_LOCKOUT_RECOVERY:
		return 0x04
	default:
		return 0xFF
	}
}
