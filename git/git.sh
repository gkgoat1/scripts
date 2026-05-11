set -eux
git "$@" || sudo git "$@" || pkexec git "$@"
