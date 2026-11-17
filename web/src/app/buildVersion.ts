// A build that is not a release reports the output of git describe: the nearest release, the commits
// since it, g and the short commit, and -dirty when the tree had changes.
const described = /^(v\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?)-\d+-g([0-9a-f]{7,40})(?:-dirty)?$/;
const bareCommit = /^([0-9a-f]{7,40})(?:-dirty)?$/;

// shortVersion is the form of a daemon's version that fits the status bar: a release as it is, a described build
// as its nearest release and short commit (v0.1.0-alpha.16+abc1234), and a build named only by its commit as dev
// and that commit. Any other version is returned as it is.
export function shortVersion(version: string): string {
  const [, release, commit] = described.exec(version) ?? [];
  if (release && commit) {
    return `${release}+${commit.slice(0, 7)}`;
  }
  const [, only] = bareCommit.exec(version) ?? [];
  return only ? `dev+${only.slice(0, 7)}` : version;
}
