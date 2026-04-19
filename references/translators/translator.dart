// kayten-proto — references/translators/translator.dart
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). This file is the single source of truth for
// that mapping — copy into consumer repos, do not re-implement.
import 'package:kayten_proto/hsm_keys/v1/hsm_keys.pb.dart';

ProvisioningState provisioningStateFromWire(int wire) {
  switch (wire & 0xFF) {
    case 0x00: return ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED;
    case 0x01: return ProvisioningState.PROVISIONING_STATE_PROVISIONED;
    case 0x02: return ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED;
    default:   return ProvisioningState.PROVISIONING_STATE_UNSPECIFIED;
  }
}

int provisioningStateToWire(ProvisioningState s) {
  switch (s) {
    case ProvisioningState.PROVISIONING_STATE_PROVISIONING_REQUIRED: return 0x00;
    case ProvisioningState.PROVISIONING_STATE_PROVISIONED:           return 0x01;
    case ProvisioningState.PROVISIONING_STATE_LOCKED_WIPED:          return 0x02;
    default:                                                         return 0xFF;
  }
}

WipeReason wipeReasonFromWire(int wire) {
  switch (wire & 0xFF) {
    case 0x00: return WipeReason.WIPE_REASON_NONE;
    case 0x01: return WipeReason.WIPE_REASON_PIN_LOCKOUT;
    case 0x02: return WipeReason.WIPE_REASON_USER_REQUEST;
    case 0x03: return WipeReason.WIPE_REASON_ATTESTATION_FAILED;
    case 0x04: return WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY;
    default:   return WipeReason.WIPE_REASON_UNSPECIFIED;
  }
}

int wipeReasonToWire(WipeReason r) {
  switch (r) {
    case WipeReason.WIPE_REASON_NONE:                 return 0x00;
    case WipeReason.WIPE_REASON_PIN_LOCKOUT:          return 0x01;
    case WipeReason.WIPE_REASON_USER_REQUEST:         return 0x02;
    case WipeReason.WIPE_REASON_ATTESTATION_FAILED:   return 0x03;
    case WipeReason.WIPE_REASON_PIN_LOCKOUT_RECOVERY: return 0x04;
    default:                                          return 0xFF;
  }
}
