package user

import (
	"context"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestUserSearchIgnoresAccents(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	for _, u := range [][2]string{{"jperez", "José Pérez"}, {"mnunez", "María Núñez"}, {"bob", "Bob"}} {
		if _, err := pool.Exec(ctx, "INSERT INTO users (username, name) VALUES ($1, $2)", u[0], u[1]); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresRepository(pool)
	for query, want := range map[string]string{"jose": "jperez", "JOSÉ": "jperez", "perez": "jperez", "nunez": "mnunez", "maría": "mnunez"} {
		users, _, err := repo.ListUsers(ctx, Filter{Query: query, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 || users[0].Username != want {
			t.Errorf("%q found %d users, want only %s", query, len(users), want)
		}
	}
}
