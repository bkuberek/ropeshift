+++
kind = "contract"
id = "release-fix"
title = "Fix the release"

[[criteria]]
id = "c1"
text = "The fix reaches the release branch"

[[work_orders]]
id = "release-fix-order"
title = "Fix the release"
repo = "."
size = "small"
base = "release/1.2"
+++

Build on the release branch, not on the default branch.
