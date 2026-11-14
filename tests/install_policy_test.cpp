#include "../examples/qt/install_policy.hpp"
#include <cassert>
int main() {
    for (int full=0;full<2;++full) for (int base=0;base<2;++base) for (int target=0;target<2;++target) {
        assert(fh_desktop_install_allowed(false,full,base,target));
        assert(fh_desktop_install_allowed(true,full,base,target) == (full && !base && !target));
    }
}
