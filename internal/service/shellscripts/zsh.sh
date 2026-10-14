ZDOTDIR=${BLACKNODE_ORIGINAL_ZDOTDIR:-$HOME}
if [[ -f "$ZDOTDIR/.zshrc" ]]; then source "$ZDOTDIR/.zshrc"; fi
if [[ ${BLACKNODE_INTEGRATED-} == 1 ]]; then return; fi
BLACKNODE_INTEGRATED=1
autoload -Uz add-zsh-hook
__blacknode_value() { printf '\033]777;blacknode;%s;%s\007' "$1" "$(printf '%s' "$2" | base64 | tr -d '\r\n')"; }
__blacknode_preexec() { printf '\033]133;C\007'; __blacknode_value command "$1"; }
__blacknode_precmd() {
  local blacknode_status=$?
  printf '\033]133;D;%s\007' "$blacknode_status"
  __blacknode_value cwd "$PWD"
  __blacknode_value branch "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || git rev-parse --short HEAD 2>/dev/null)"
  printf '\033]133;A\007'
}
add-zsh-hook preexec __blacknode_preexec
add-zsh-hook precmd __blacknode_precmd
