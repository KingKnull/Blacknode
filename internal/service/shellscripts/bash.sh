if [ -f "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi
if [ "${BLACKNODE_INTEGRATED-}" = 1 ]; then return; fi
BLACKNODE_INTEGRATED=1
__blacknode_value() { printf '\033]777;blacknode;%s;%s\007' "$1" "$(printf '%s' "$2" | base64 | tr -d '\r\n')"; }
__blacknode_start() {
  printf '\033]133;C\007'
  __blacknode_value command "$(HISTTIMEFORMAT= history 1 | sed 's/^[[:space:]]*[0-9]*[[:space:]]*//')"
}
__blacknode_prompt() {
  local blacknode_status=$?
  printf '\033]133;D;%s\007' "$blacknode_status"
  __blacknode_value cwd "$PWD"
  __blacknode_value branch "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || git rev-parse --short HEAD 2>/dev/null)"
  printf '\033]133;A\007'
}
PS0='$(__blacknode_start)'"${PS0-}"
if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == 'declare -a'* ]]; then
  PROMPT_COMMAND=(__blacknode_prompt "${PROMPT_COMMAND[@]}")
else
  PROMPT_COMMAND="__blacknode_prompt${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
fi
