# Issues Log

## 2026-05-24 · nsjail submodule git errors during Docker build

**What we were trying to do:**
Build the Docker image with nsjail compiled from source as a git submodule.

**What went wrong:**
The nsjail Makefile runs `git submodule update --init` for its kafel dependency, but inside Docker the .git metadata is incomplete, so it fails with "not a git repository" errors.

**How we resolved it:**
Ran `git submodule update --init` on the host before building, so kafel is already populated when Docker copies the files. The Makefile sees kafel/Makefile exists and skips the git commands.

**What we learned:**
Docker COPY doesnt preserve git submodule metadata. Pre-populate submodule dependencies on the host first.

## 2026-05-25 · nsjail leaking UID warnings into stderr

**What we were trying to do:**
Capture clean stderr from user programs without nsjail's internal chatter bleeding into it.

**What went wrong:**
nsjail prints "[W] logParams():313 Process will be UID=0" to stderr, which gets captured alongside user program stderr and breaks our JSON response parsing.

**How we resolved it:**
Added --log /dev/null and -Q flags to suppress nsjail's internal logging while keeping the user process's actual stderr output intact through the IPC pipes.

**What we learned:**
nsjail mixes its own diagnostics with user stderr by default. You have to explicitly silence it.
