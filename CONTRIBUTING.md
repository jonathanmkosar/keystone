# Contributing to Keystone

Thanks for your interest in Keystone. This is a personal project built in the open, and contributions of any kind are welcome — bug reports, documentation improvements, new stones, code fixes, or ideas in issues.

## Project status

Keystone is in early development. Architecture and APIs may change without notice until the first tagged release. If you're building something on top of it, expect breakage.

## Ways to contribute

**Report bugs.** Open an issue with clear reproduction steps, your environment (OS, Python/Node version, Claude client), and what you expected versus what happened.

**Request features.** Open an issue describing the use case first, not the implementation. Let's talk about the problem before jumping to code.

**Improve documentation.** Typo fixes, clarifications, missing setup steps, better examples — all welcome via PR without prior discussion.

**Build a stone.** Keystone is designed for new stones to live outside the core runtime. If you build one, consider publishing it as its own repo and linking back. Happy to link interesting stones in the README.

**Fix bugs.** For small fixes, just open a PR. For anything structural, open an issue first.

## Before you open a PR

- Check there isn't an existing issue or PR for the same thing.
- For anything beyond a small fix or doc change, open an issue first and get a thumbs-up before investing time.
- Keep PRs focused on one thing. "Fixed bug X and also refactored module Y" should be two PRs.
- Match the existing code style.
- Add or update tests where relevant.
- Update documentation where behavior changes.

## Development setup

See `README.md` for environment setup. In short:

1. Clone the repo
2. Install [uv](https://docs.astral.sh/uv/) and run `uv sync`
3. Copy `.env.example` to `.env` and fill in any local overrides
4. Bootstrap the reference stones: `uv run python scripts/bootstrap_stones.py`
5. Run a stone over stdio: `uv run keystone --stone <stone_name> --transport stdio`
6. Point Claude Desktop at the local runtime via its config file (see the README for an example)
7. Run the test suite with `uv run pytest`

## Contributor License Agreement

By submitting a pull request, you agree that:

1. You wrote the code yourself, or have the right to submit it.
2. You grant the project maintainer (Jon Kosar), and his successors and assignees (including any entity to which the project is later transferred, such as an LLC formed to steward Keystone), a perpetual, worldwide, non-exclusive, royalty-free, irrevocable, sublicensable, and transferable license to use, reproduce, modify, prepare derivative works of, sublicense, relicense, and distribute your contribution, in whole or in part, as part of Keystone or any derivative project, under any license terms (including proprietary or commercial dual-licensing) the maintainer chooses now or in the future.
3. Your contribution will be released to the public under the same license as the project (currently FSL-1.1-Apache-2.0). The grant in (2) is in addition to that public release and exists so the project can evolve its license without re-collecting CLAs from past contributors.

This is a lightweight CLA so the project can evolve its license if needed (e.g., dual licensing for commercial use, or transferring stewardship to an LLC). It does not transfer copyright — you still own your contribution and may use it however you like elsewhere.

## Code of conduct

Be respectful, constructive, and patient. Disagreements are fine; contempt isn't. Assume good intent. If something feels off, flag it to the maintainer privately.

Harassment, discrimination, or personal attacks won't be tolerated and will result in removal from the project.

## Communication

- **Bugs and features:** GitHub Issues
- **Questions:** GitHub Discussions (once enabled)
- **Security vulnerabilities:** Email the maintainer directly instead of opening a public issue

## License

Keystone is licensed under the Functional Source License 1.1 with Apache 2.0 as the change license. By contributing, you agree your contributions will be licensed under the same terms. See `LICENSE` for full details.

## Thanks

This project exists because personal data should belong to the person who generated it, not to whichever cloud most recently captured it. Thanks for helping build toward that.
