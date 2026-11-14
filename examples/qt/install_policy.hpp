#pragma once
// FH1 signs complete artifact bytes, but does not bind outer file destinations.
inline bool fh_desktop_install_allowed(bool signedMode, bool fullPackage,
                                       bool hasBaseVersion, bool hasTargetPath) {
    return !signedMode || (fullPackage && !hasBaseVersion && !hasTargetPath);
}
