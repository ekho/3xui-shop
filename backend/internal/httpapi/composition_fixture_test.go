package httpapi

import (
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/wire"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
)

type compositionFixture struct {
	*API
	*app.Modules
}

func composeForTest(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg app.Config) *compositionFixture {
	modules := app.NewModules(pool, limiter, queue, &cfg)
	contract, err := wire.GetSwagger()
	if err != nil {
		panic(err)
	}
	return &compositionFixture{API: newAPI(modules, pool, cfg.HTTP, contract), Modules: modules}
}
