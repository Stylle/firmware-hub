/** @file fw_client.hpp
 * @brief C++17 wrapper around the portable FH1 C policy layer.
 * @note Network transport and installation remain application responsibilities.
 * Verification returns error codes; standard container allocation can still
 * throw std::bad_alloc. Use the C interface for strictly heap-free integration.
 */
#ifndef FW_CLIENT_HPP
#define FW_CLIENT_HPP
#include "../c/fw_client.h"
#include <array>
#include <string>
#include <vector>
namespace firmware {
class Client {
  public:
    /** Copy public policy values. Pointer members must outlive this Client. */
    explicit Client(const fw_policy &policy) : policy_(policy) {}
    /** Verify a signed manifest using a provisioned key and a vetted backend. */
    fw_result verify(const std::vector<uint8_t> &payload, const std::array<uint8_t, 64> &signature,
                     const std::array<uint8_t, 32> &trustedRoot, fw_ed25519_verify_fn backend,
                     void *context, fw_manifest &out) const;
    /** Build a relative check URL; returns empty on invalid configuration. */
    std::string checkPath() const;
    /** Explicit account mode: trusts HTTPS Hub metadata; not a digital signature. */
    fw_result acceptServerManifest(const std::vector<uint8_t> &payload, fw_manifest &out) const {
        return fw_accept_server_manifest(payload.data(), payload.size(), &policy_, &out);
    }
    /** Change only local telemetry consent. This never enables Hub consent. */
    void setTelemetry(bool enabled) noexcept {
        policy_.telemetry_enabled = enabled ? 1 : 0;
    }
    /** Inspect the immutable policy copy for an application transport adapter. */
    const fw_policy &policy() const noexcept {
        return policy_;
    }

  private:
    fw_policy policy_;
};
} // namespace firmware
#endif
