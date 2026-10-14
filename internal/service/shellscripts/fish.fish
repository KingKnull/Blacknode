if set -q BLACKNODE_INTEGRATED
  return
end
set -g BLACKNODE_INTEGRATED 1
function __blacknode_value
  printf '\e]777;blacknode;%s;%s\a' $argv[1] (printf '%s' "$argv[2]" | base64 | string replace -ar '[\r\n]' '')
end
function __blacknode_preexec --on-event fish_preexec
  printf '\e]133;C\a'
  __blacknode_value command "$argv[1]"
end
function __blacknode_postexec --on-event fish_postexec
  set -l blacknode_status $status
  printf '\e]133;D;%s\a' $blacknode_status
end
function __blacknode_prompt --on-event fish_prompt
  __blacknode_value cwd "$PWD"
  set -l branch (git symbolic-ref --quiet --short HEAD 2>/dev/null; or git rev-parse --short HEAD 2>/dev/null)
  __blacknode_value branch "$branch"
  printf '\e]133;A\a'
end
