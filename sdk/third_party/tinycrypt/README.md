# SHA-256 reference dependency

Unmodified `sha256.c`, `utils.c` and their required headers from Intel TinyCrypt, commit `cf24c907a61c01bc2c4e1ee0ae24457c27a840a2`:

https://github.com/intel/tinycrypt/tree/cf24c907a61c01bc2c4e1ee0ae24457c27a840a2

BSD-3-Clause; upstream notices and LICENSE retained. Used ONLY for SHA-256 in the minimal MCU reference and host verification. No ECC/AES/PRNG code or authentication claims are imported. Upstream is archived; this fixed, narrowly scoped component is not a claim that the entire archived library is maintained or suitable for a production security stack. The example includes a known SHA-256 vector and full staged-image checks. Select a maintained platform crypto backend for production hardware.
