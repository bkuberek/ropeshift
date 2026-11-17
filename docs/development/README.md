# Development

These pages are for the people who build, test, and release ropeshift. [AGENTS.md](../../AGENTS.md) lists the layout, the dependency rules, and every make target, and the [user documentation](../../README.md#documentation) describes the product.

- [Making a release](releasing.md): the tools and make targets a release needs, continuous integration, how a release happens, and releasing by hand.
- [Choosing a version](versioning.md): how each release's version is chosen, and how a build is stamped with it.
- [Schema migrations](schema-migrations.md): how each database reaches a build's schema, the checksum rule that refuses a changed migration, and the warning for one a build does not ship.
- [Web tests](web-tests.md): the accessibility and keyboard suites, and which browser they run in.
- [Screenshots](screenshots.md): how the images of the web UI and `ropeshift top` are captured on a sample project.
- [The Postgres adapter](postgres.md): how it uses connections, the migration lock, and the timeouts a write is bound by.
