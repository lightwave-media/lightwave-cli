# shellcheck shell=bash
# Sourced by every script under scripts/ that builds a git fixture. The shell
# twin of internal/testutil/gitfixture; its package doc tells the incident.
#
# A git hook exports GIT_DIR and friends pointing at the repo that ran it, and
# they outrank cd and -C for every git command, so a fixture's commits and
# config writes would land there. `git config` also honours GIT_CONFIG, which
# git never exports but any other caller can set.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR \
  GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES \
  GIT_CONFIG GIT_CONFIG_PARAMETERS

# Identity is exported, never written with `git config user.*`: a written
# identity outlives the script in whatever repo git resolved, and authors later
# commits made there. An exported variable reaches only this process and its
# children.
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@lightwave.invalid
export GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@lightwave.invalid
