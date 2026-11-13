# Third-party verification backend

Monocypher **4.0.3**, unchanged upstream sources, used for standard Ed25519 verification in the Qt reference adapter and SDK tests. The portable C SDK can instead use a platform cryptographic backend. Preserve `monocypher/LICENCE.md` when redistributing.

Official source: https://monocypher.org/download/monocypher-4.0.3.tar.gz

SHA-512:
```
40904ada5c7ee4f7741733e38b69a30a4b0561cbffba5ffe7c2dce16136d540251ec0d9056ff606510d3b5b708fb8a40db7e0870d4a0b2dc17ba2bfb880f8965
```

Only `src/monocypher.{c,h}`, `src/optional/monocypher-ed25519.{c,h}` and the license are included. The known upstream comparison-branch caveat does not expose a secret here: only public-key signature verification and its public success/failure result are used. The Hub signing tool uses Go's standard Ed25519 implementation, not this backend.
