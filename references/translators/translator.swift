// kayten-proto — references/translators/translator.swift
// Mechanical wire↔enum translator. proto3 forces _UNSPECIFIED = 0, so enum
// numeric values are (wire + 1). Single source of truth for the mapping —
// copy into consumer repos, do not re-implement.
//
// Assumes swift-protobuf-generated ProvisioningState / WipeReason enums
// from kayten.hsm_keys.v1.hsm_keys.proto.
import Foundation

public func provisioningStateFromWire(_ w: UInt8) -> ProvisioningState {
    switch w {
    case 0x00: return .provisioningStateProvisioningRequired
    case 0x01: return .provisioningStateProvisioned
    case 0x02: return .provisioningStateLockedWiped
    default:   return .provisioningStateUnspecified
    }
}

public func provisioningStateToWire(_ s: ProvisioningState) -> UInt8 {
    switch s {
    case .provisioningStateProvisioningRequired: return 0x00
    case .provisioningStateProvisioned:           return 0x01
    case .provisioningStateLockedWiped:           return 0x02
    default:                                       return 0xFF
    }
}

public func wipeReasonFromWire(_ w: UInt8) -> WipeReason {
    switch w {
    case 0x00: return .wipeReasonNone
    case 0x01: return .wipeReasonPinLockout
    case 0x02: return .wipeReasonUserRequest
    case 0x03: return .wipeReasonAttestationFailed
    case 0x04: return .wipeReasonPinLockoutRecovery
    default:   return .wipeReasonUnspecified
    }
}

public func wipeReasonToWire(_ r: WipeReason) -> UInt8 {
    switch r {
    case .wipeReasonNone:               return 0x00
    case .wipeReasonPinLockout:         return 0x01
    case .wipeReasonUserRequest:        return 0x02
    case .wipeReasonAttestationFailed:  return 0x03
    case .wipeReasonPinLockoutRecovery: return 0x04
    default:                             return 0xFF
    }
}
