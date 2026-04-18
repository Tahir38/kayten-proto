// kayten-proto — references/translators/translator.rs
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). Single source of truth for the mapping —
// copy into consumer repos, do not re-implement.
//
// Consumer note: `kayten_proto` crate comes from prost-generated output of
// kayten.hsm_keys.v1.hsm_keys.proto. Variant names follow prost defaults.

use kayten_proto::hsm_keys::v1::{ProvisioningState, WipeReason};

pub fn provisioning_state_from_wire(w: u8) -> ProvisioningState {
    match w {
        0x00 => ProvisioningState::ProvisioningRequired,
        0x01 => ProvisioningState::Provisioned,
        0x02 => ProvisioningState::LockedWiped,
        _    => ProvisioningState::Unspecified,
    }
}

pub fn provisioning_state_to_wire(s: ProvisioningState) -> u8 {
    match s {
        ProvisioningState::ProvisioningRequired => 0x00,
        ProvisioningState::Provisioned          => 0x01,
        ProvisioningState::LockedWiped          => 0x02,
        _                                       => 0xFF,
    }
}

pub fn wipe_reason_from_wire(w: u8) -> WipeReason {
    match w {
        0x00 => WipeReason::None,
        0x01 => WipeReason::PinLockout,
        0x02 => WipeReason::UserRequest,
        0x03 => WipeReason::AttestationFailed,
        0x04 => WipeReason::PinLockoutRecovery,
        _    => WipeReason::Unspecified,
    }
}

pub fn wipe_reason_to_wire(r: WipeReason) -> u8 {
    match r {
        WipeReason::None               => 0x00,
        WipeReason::PinLockout         => 0x01,
        WipeReason::UserRequest        => 0x02,
        WipeReason::AttestationFailed  => 0x03,
        WipeReason::PinLockoutRecovery => 0x04,
        _                              => 0xFF,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn round_trip_provisioning_state() {
        for wire in &[0x00u8, 0x01, 0x02] {
            assert_eq!(provisioning_state_to_wire(provisioning_state_from_wire(*wire)), *wire);
        }
    }
    #[test]
    fn round_trip_wipe_reason() {
        for wire in &[0x00u8, 0x01, 0x02, 0x03, 0x04] {
            assert_eq!(wipe_reason_to_wire(wipe_reason_from_wire(*wire)), *wire);
        }
    }
    #[test]
    fn unknown_wire_maps_to_unspecified() {
        assert!(matches!(provisioning_state_from_wire(0xAA), ProvisioningState::Unspecified));
        assert!(matches!(wipe_reason_from_wire(0xBB), WipeReason::Unspecified));
    }
}
