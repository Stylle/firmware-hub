#include "fw_client.hpp"
namespace firmware {
fw_result Client::verify(const std::vector<uint8_t> &p, const std::array<uint8_t, 64> &s,
                         const std::array<uint8_t, 32> &k, fw_ed25519_verify_fn backend, void *ctx,
                         fw_manifest &out) const {
    return fw_verify_manifest(p.data(), p.size(), s.data(), k.data(), &policy_, backend, ctx, &out);
}
std::string Client::checkPath() const {
    char path[1024];
    return fw_make_check_path(&policy_, path, sizeof(path)) == FW_OK ? std::string(path)
                                                                     : std::string();
}
} // namespace firmware
