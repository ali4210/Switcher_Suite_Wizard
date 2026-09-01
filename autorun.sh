#!/usr/bin/env bash

# ==============================================================================
# SWITCHER SUITE WIZARD — ENTERPRISE SELF-HEALING MULTI-OS BUILD WRAPPER
#
# Usage:
#   ./autorun.sh           Interactive OS Selector, validation, & launch
#   ./autorun.sh -f        Force rebuild: clean build/test cache + old binary
#   ./autorun.sh --hard    Full purge: clean Go caches, rebuild from vendor/cache
#   ./autorun.sh --help    Display help screen
# ==============================================================================

set -euo pipefail
IFS=$'\n\t'

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-normal}"

# ------------------------------------------------------------------------------
# Terminal Colors
# ------------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# ------------------------------------------------------------------------------
# Display Helpers
# ------------------------------------------------------------------------------
print_line() {
    printf "${CYAN}==============================================================================${NC}\n"
}

info() {
    printf "${CYAN}[+]${NC} %s\n" "$1"
}

success() {
    printf "${GREEN}[✓]${NC} %s\n" "$1"
}

warning() {
    printf "${YELLOW}[!]${NC} %s\n" "$1"
}

error() {
    printf "${RED}[✗]${NC} %s\n" "$1" >&2
}

step() {
    printf "\n${BOLD}${CYAN}--- %s ---${NC}\n" "$1"
}

cleanup_on_error() {
    exit_code=$?
    printf "\n"
    error "Build process stopped with exit code ${exit_code}."
    warning "Fix the error shown above, then run the script again."
    exit "$exit_code"
}

trap cleanup_on_error ERR

cd "$PROJECT_DIR"
clear

# ------------------------------------------------------------------------------
# Display Portrait Banner
# ------------------------------------------------------------------------------
echo -e "${CYAN}${BOLD}"
cat << 'EOF'
                                                                                                .       
       .                                              ............     .                        .       
      .                                   ...::-===+=+***++++++=-:..         .  . .       . .           
                .              .....:=#%%%@@@@@@@@@@@@@@%@@@%#*=:..                                 
                       .  . ....:=+#%@@@@%@@@@@@@@@@@@@@@@@@@@@@%#*=..                              
            .               .:+#@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@#@#=...  .                       
              .           .:+#@@@@%%@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@%%%*:..     .            ..    
         .              ..-*%%@%@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@%+..                       
       .               ..+%%@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@%@@@@@@@@@@@@@@-...                     
                     ...#%%%@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@#+..    .                
.                    ..:%%%@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@%#-.                  .   
         .           ..*%%@@@@@@@@@%@@@@@@@@@@@@@@@@%##****#@@@@@@@@@@@@@@@@%-.                     
                     .=%@@@@@@@@@@@@@@@@%%###*###***++=======++#%@@@@@@@@@@@@*...                   
          .          .+%@@@@@@@%@@@@%#+====-========-------=====++*#@@@@@@@@%*:.. .                 
                     .:%@@@@@@@@@@%*===-----------------------====++#@@@@@@@@-...     .          .  
                     ..*@@@@@@@%*+====-------------------------====++%@@@@@@@-..               .    
                     ..=%@@@@@%*=====-----:::::----------------=====+*@@@@@@%..                     
                     ...+%%@@@#+===--------:::::---------------======+%@@@@%:..  .                . 
                     ...:%@@@@#+==----------::::--------------=======+*@@@@-...     .                
                       ..%@@@#+===--==---:::::::::::::::::---==+++====+*@@@:...   .            .    
                       ..+@@%+=====*###%#*+==-::::::::::-=+*%%%@@%%*+==+%@#....               .    .
                      . .-@@%+====***+++++++=--::::::---=+++++=+++**+===#@=....         .   .       
       .             ....=#@#========----===+==---:---======---=====++==*@#=...              .      
                     ...+=+%#=======++=======++=-----===========++++====*%*+...                     
                  .   ...+==#*=----=+#*=%#%#*+====----====++#%@@=+#*=====+*++-..                    
                    ....=-=#+==----=====**====------========+*==++===-===#*==..                     
                  .  ...:==#+=----------==-----------==----======------==*#+:..                .     
                     ....-=*+=-----------------------==----------------==*#+...                     
                      ...:=**=-----------------------==----------------==#*+... .                   
                        .:=*#+=---------::::--==-----===--:-----------==*#+=...                     
                        ..=*%*==------::::-----=-::--==----:::-------==+#%*:...                     
                        ..-*%%+===----::-----====--=++++=------------==*%%*:...                     
     .                  ...-#%*+==----:-----=+*+++++***+=----------===+#%#-....            .        
                        ...:+%%*+==------=+****+++++++###*+==----===++*%%*=:...   .                 
    .                   ...:+%%%*++=---==**+===========++****+=====++*#%*+-..                       
   .      .             ..:=*#%%%**+==-=+++**+++======+******+===++*#%%#==:..                       
           .            ..-=+*#%%%#**====---==++=====++++==-=+=++*#%%%%*=-...                .      
                        ..:-+###%%%%#++==---==**#%%%##*+=====++**#%%%%%+-:..     .                  
                      . ...:++++#%@%%#*+======+*#%%#*+==-===+**#%@@@%**+:...                        
                        ....:-=+%%%%%%%#*+===++*+*+==++===++*#%%%@@%%=-:....                        
                     .    ...:+#%%%%%@@@%#**+*++++++**#***##%%@@@%%%%#=.....                        
                   .        .=#%%#%%%%%@%%%%%%#%%#%%%%%%%%%%%@@@%%%%%%##+:..  .                     
                     .......+*#%%%%%%%%@@@@%%%%@@@@@@@@@@@@@@@%%%%%%%%####*-.......                 
            . ..........:=*#*##%%#%%%%%%@%%%%@@@@@@@%%@@@@%%%%%%%%%%%%######***=.......... .        
              .......-+**###*##%%##%%%%%%%%%%%%%@@@%@@@%%%%%%%%%%%%%%##%%########*+-......          
   .   ..........:++***########%%#####%%%%%%%%%%@%%%%%@%%%%%%%%%%%%%%%######%##%###***+-.........   
      ......:-+******##########%@#**####%%%%%%%%%%%@%%%%%%%%%%%%%%%%%%%%%#############*#**+-:...... 
.........:=+**##################%%#*#####%%%%%%%%@@%%%%%%%%%%%%%%%%%%%%%%%%%%%#######*****###**=:...
.....:-+++******####################***###%#%%%%%%%%%%%%%%%%%%%%%%%%%%%%%#%%%#####*##***********#**+
.:-=+***###******##**##*######################%%%%%%%%%%%%%%%%%%%%%%%#############**************###*
+*******#*##*****#####################%###%%%########%%%%%%%%%%%%%%##%#####%##########***********##*
*******#####*#***#*###*################%%%%#%%%%#***#%%%%%##%%%%%%################**#*###****##**###
********#*#***######*#######*########%%%%%%%####%#**########%%%%%%######%########************#***###
****#########******#####*###############%%%%#######**###%%#####################********##**#**##****
****##**#######*******##*######################%%%#####%%########################**#*##*####**#**###
*****##*###*****#***###**#*################%###%%%%%%%%%####*#################***#**#***##******####
#****##*#*****#**####*#**#*##*####*##*#########%%%%%%###########*####**#########*****#*###***#****#*
*#***#**#*##*#****#***##***#*#########**########%%%%%########**#####****########******###**##**#*#**
*#****#*####*******#**###*****######*##*######%#%%############**##*#**#######*#****#####*###****#***
****###*####**##********#*###*########*###**##%%###**#############*####*#****##*###########**###***#
**#**##**###****######*####***#*#####*######**%######*##########**#####*###*###*##*######***###**###
####**###########*##*#####*###*########*######%####**########**#*###*#*###**#####################***
##****###*####**#**#*###***###**######**##*###%########%#####***##########****##############*#######
#*##*##########*******###*####**########**####%#####*#####******#####*#*###############*############
%##*##*#########**#########*##*######**#####%#%#####*#######**####*##############%#########*########
#################*###**#*##############################%###*#*########**#*####%#%#**####%%#########%
%%###*########%##*#######**##############%#################****########**#####%%%##*##############%%
#%%##########%%###*#######*#########################################**#######%%#########%%########%%
EOF
echo -e "${NC}"

# ------------------------------------------------------------------------------
# Help Screen Handler
# ------------------------------------------------------------------------------
if [[ "$MODE" == "-h" || "$MODE" == "--help" || "$MODE" == "help" ]]; then
    printf "\n"
    print_line
    printf "${BOLD}${CYAN}                    SWITCHER SUITE WIZARD — BUILD SYSTEM${NC}\n"
    print_line
    printf "\n"
    printf "${BOLD}Usage:${NC}\n"
    printf "  ./autorun.sh            Interactive Multi-OS selector, build, and launch\n"
    printf "  ./autorun.sh -f         Force rebuild with build/test cache cleanup\n"
    printf "  ./autorun.sh --hard     Full cache purge and clean rebuild\n"
    printf "  ./autorun.sh --help     Display this help screen\n\n"
    printf "${BOLD}Features:${NC}\n"
    printf "  => Multi-OS Target Wrapper: Build natively or cross-compile on demand.\n"
    printf "  => Offline-Resilient: Compiles directly via vendor directory if offline.\n"
    printf "  => Zero-Latency Global CLI: 'switcher-wizard' bypasses this wrapper completely.\n\n"
    exit 0
fi

echo -e "${CYAN}╔════════════════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║${NC} ${BOLD}TARGET OPERATING SYSTEM SELECTION WRAPPER${NC}                                  ${CYAN}║${NC}"
echo -e "${CYAN}╠════════════════════════════════════════════════════════════════════════════╣${NC}"
echo -e "${CYAN}║${NC} [1] => ${GREEN}Linux (Debian / Ubuntu / Kali / RHEL / CentOS / Arch)${NC}               ${CYAN}║${NC}"
echo -e "${CYAN}║${NC} [2] => ${GREEN}Microsoft Windows (x64 / Win10 / Win11 / Server)${NC}                    ${CYAN}║${NC}"
echo -e "${CYAN}║${NC} [3] => ${GREEN}Apple macOS (Darwin Subsystem / amd64 & arm64)${NC}                      ${CYAN}║${NC}"
echo -e "${CYAN}║${NC} [4] => ${YELLOW}Auto-Detect Host Platform & Launch Immediately${NC}                      ${CYAN}║${NC}"
echo -e "${CYAN}║${NC} [Q] => Exit Build System                                                   ${CYAN}║${NC}"
echo -e "${CYAN}╚════════════════════════════════════════════════════════════════════════════╝${NC}"
echo ""
OS_CHOICE=""
read -rp " Select Target Operating System [1-4 / Q]: " OS_CHOICE || true

case "$OS_CHOICE" in
    1)
        TARGET_OS="linux"
        TARGET_ARCH="amd64"
        BINARY_NAME="Switcher_Suite_Wizard"
        ;;
    2)
        TARGET_OS="windows"
        TARGET_ARCH="amd64"
        BINARY_NAME="Switcher_Suite_Wizard.exe"
        ;;
    3)
        TARGET_OS="darwin"
        TARGET_ARCH="amd64"
        BINARY_NAME="Switcher_Suite_Wizard_macOS"
        ;;
    4)
        case "$(uname -s)" in
            Linux*)  TARGET_OS="linux"; TARGET_ARCH="amd64"; BINARY_NAME="Switcher_Suite_Wizard" ;;
            Darwin*) TARGET_OS="darwin"; TARGET_ARCH="amd64"; BINARY_NAME="Switcher_Suite_Wizard_macOS" ;;
            *)       TARGET_OS="linux"; TARGET_ARCH="amd64"; BINARY_NAME="Switcher_Suite_Wizard" ;;
        esac
        ;;
    [qQ])
        echo -e "\n${YELLOW}=> Operation cancelled by user.${NC}"
        exit 0
        ;;
    *)
        echo -e "\n${RED}[!] Invalid choice. Defaulting to Linux.${NC}"
        TARGET_OS="linux"
        TARGET_ARCH="amd64"
        BINARY_NAME="Switcher_Suite_Wizard"
        ;;
esac

# ------------------------------------------------------------------------------
# Step 2: Verify Go Installation
# ------------------------------------------------------------------------------
step "CHECKING GO TOOLCHAIN"

if ! command -v go >/dev/null 2>&1; then
    error "Go is not installed or is not available in PATH."
    error "Install Go from https://go.dev/dl/ and run this script again."
    exit 1
fi

success "Go compiler found: $(go version)"

if [[ ! -f "go.mod" ]]; then
    warning "go.mod was not found. Initializing module 'switcher'..."
    go mod init switcher >/dev/null 2>&1 || true
fi

success "Go module file confirmed."

# ------------------------------------------------------------------------------
# Step 3: Cleaning Modes
# ------------------------------------------------------------------------------
case "$MODE" in
    normal)
        step "NORMAL BUILD MODE"
        info "Keeping Go caches for a faster build."
        ;;
    -f)
        step "FORCE REBUILD MODE"
        warning "Removing previous compiled binary..."
        rm -f "$BINARY_NAME"
        warning "Cleaning Go build cache and test cache..."
        go clean -cache -testcache
        success "Force-clean complete."
        ;;
    --hard)
        step "HARD PURGE MODE"
        warning "Removing compiled binary and purging build/test/fuzz caches..."
        rm -f "$BINARY_NAME"
        go clean -cache
        go clean -testcache
        go clean -fuzzcache
        success "Hard purge complete."
        ;;
esac

# ------------------------------------------------------------------------------
# Step 4: Format Source Files
# ------------------------------------------------------------------------------
step "FORMATTING GO SOURCE"

info "Running gofmt on project Go files..."
GO_FILES=()
while IFS= read -r -d '' file; do
    GO_FILES+=("$file")
done < <(find . -maxdepth 1 -type f -name "*.go" -print0)

if [[ ${#GO_FILES[@]} -gt 0 ]]; then
    gofmt -w "${GO_FILES[@]}"
    success "Go source formatting complete."
fi

# ------------------------------------------------------------------------------
# Step 5: Dependency Synchronization (Offline Resilient)
# ------------------------------------------------------------------------------
step "SYNCING GO DEPENDENCIES"

BUILD_VENDOR_FLAG=""
if [[ -d "$PROJECT_DIR/vendor" ]]; then
    success "Local vendor directory detected. Enabling 100% OFFLINE compilation mode."
    BUILD_VENDOR_FLAG="-mod=vendor"
else
    info "Attempting to sync dependencies with go mod tidy..."
    if go mod tidy 2>/dev/null; then
        success "Dependencies synchronized online."
    else
        warning "Network unreachable. Using local module cache to compile..."
    fi
fi

# ------------------------------------------------------------------------------
# Step 6: Static Analysis & Tests
# ------------------------------------------------------------------------------
step "RUNNING STATIC ANALYSIS"

info "Running go vet..."
if [[ -n "$BUILD_VENDOR_FLAG" ]]; then
    go vet -mod=vendor ./...
else
    go vet ./...
fi
success "Static analysis completed with no reported issues."

step "RUNNING TESTS"
if find . -maxdepth 1 -type f -name "*_test.go" -print -quit | grep -q .; then
    info "Test files found. Running tests..."
    if [[ -n "$BUILD_VENDOR_FLAG" ]]; then
        go test -mod=vendor ./...
    else
        go test ./...
    fi
    success "All tests passed."
else
    warning "No *_test.go files found. Skipping unit tests."
fi

# ------------------------------------------------------------------------------
# Step 7: Target Compilation
# ------------------------------------------------------------------------------
step "COMPILING TARGET ENGINE: ${TARGET_OS}/${TARGET_ARCH}"
info "Building binary: $BINARY_NAME (Optimized Release Build)"

if [[ -n "$BUILD_VENDOR_FLAG" ]]; then
    GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o "$BINARY_NAME" .
else
    GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BINARY_NAME" .
fi

if [[ ! -f "$BINARY_NAME" ]]; then
    error "Build command completed but the binary was not created."
    exit 1
fi

chmod +x "$BINARY_NAME" 2>/dev/null || true
success "Compilation successful."
success "Binary ready: $PROJECT_DIR/$BINARY_NAME"

# ------------------------------------------------------------------------------
# Step 8: Execution vs. Cross-Compilation Handoff
# ------------------------------------------------------------------------------
HOST_UNAME="$(uname -s)"

if [[ "$TARGET_OS" == "linux" && "$HOST_UNAME" == "Linux" ]]; then
    step "LAUNCHING SWITCHER SUITE"
    printf "${DIM}Executing with Root Privileges...${NC}\n\n"
    if [[ "$(id -u)" -eq 0 ]]; then
        "./$BINARY_NAME"
    else
        sudo "./$BINARY_NAME"
    fi
    printf "\n"
    success "Switcher Suite closed. Returned safely to your terminal."
elif [[ "$TARGET_OS" == "darwin" && "$HOST_UNAME" == "Darwin" ]]; then
    step "LAUNCHING SWITCHER SUITE"
    printf "${DIM}Executing with Root Privileges...${NC}\n\n"
    if [[ "$(id -u)" -eq 0 ]]; then
        "./$BINARY_NAME"
    else
        sudo "./$BINARY_NAME"
    fi
    printf "\n"
    success "Switcher Suite closed. Returned safely to your terminal."
else
    printf "\n"
    print_line
    printf "${BOLD}${GREEN}[✓] CROSS-COMPILATION COMPLETE!${NC}\n"
    printf "${YELLOW}[i] Standalone executable package generated for '%s'.${NC}\n" "$TARGET_OS"
    printf "${YELLOW}[i] File ready for deployment: %s/%s${NC}\n" "$PROJECT_DIR" "$BINARY_NAME"
    print_line
    printf "\n"
fi