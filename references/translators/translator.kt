// kayten-proto — references/translators/translator.kt
package com.kayten.proto.hsm_keys.v1

import kayten.hsm_keys.v1.HsmKeys.ProvisioningState
import kayten.hsm_keys.v1.HsmKeys.WipeReason

fun provisioningStateFromWire(wire: Byte): ProvisioningState = when (wire.toInt() and 0xFF) {
  0x00 -> ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED
  0x01 -> ProvisioningState.PROVISIONING_STATE_PROVISIONED
  0x02 -> ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED
  else -> ProvisioningState.PROVISIONING_STATE_UNSPECIFIED
}

fun provisioningStateToWire(s: ProvisioningState): Int = when (s) {
  ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED -> 0x00
  ProvisioningState.PROVISIONING_STATE_PROVISIONED           -> 0x01
  ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED          -> 0x02
  else                                                        -> 0xFF
}

fun wipeReasonFromWire(wire: Byte): WipeReason = when (wire.toInt() and 0xFF) {
  0x00 -> WipeReason.WIPE_REASON_NONE
  0x01 -> WipeReason.WIPE_REASON_PIN_LOCKOUT
  0x02 -> WipeReason.WIPE_REASON_USER_REQUEST
  0x03 -> WipeReason.WIPE_REASON_ATTESTATION_FAILED
  0x04 -> WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY
  else -> WipeReason.WIPE_REASON_UNSPECIFIED
}

fun wipeReasonToWire(r: WipeReason): Int = when (r) {
  WipeReason.WIPE_REASON_NONE                 -> 0x00
  WipeReason.WIPE_REASON_PIN_LOCKOUT          -> 0x01
  WipeReason.WIPE_REASON_USER_REQUEST         -> 0x02
  WipeReason.WIPE_REASON_ATTESTATION_FAILED   -> 0x03
  WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY -> 0x04
  else                                         -> 0xFF
}
