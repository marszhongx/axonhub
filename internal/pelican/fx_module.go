package pelican

import "go.uber.org/fx"

// Module registers the pelican test module. Adding it to the application is a single line in
// internal/server/server.go, which keeps the module easy to drop, keep or rebase.
//
// Constructors take concrete types only: dependency injection resolves parameters by type,
// so interfaces (ChatCompleter) stay internal to the package and are useful for tests.
var Module = fx.Module("pelican",
	fx.Provide(
		NewStore,
		NewGateway,
		NewRunner,
		NewService,
		NewHandlers,
	),
)
