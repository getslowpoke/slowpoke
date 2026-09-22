package sqlsafe

import "testing"

func TestExplainable(t *testing.T) {
	ok := []string{
		"select * from t where a = 1",
		"  SELECT 1",
		"with x as (select 1) select * from x",
		"select * from logs where msg = 'please delete; update later?'",
	}
	bad := []string{
		"update t set a = 1",
		"select 1; drop table t",
		"select * from t where a = ?",
		"select * from t where a = $1",
		"select * from t for update",
		"select sleep(10)",
		"select * into outfile '/tmp/x' from t",
		"with d as (delete from t returning *) select * from d",
	}
	for _, s := range ok {
		if !Explainable(s) {
			t.Errorf("should be explainable: %q", s)
		}
	}
	for _, s := range bad {
		if Explainable(s) {
			t.Errorf("must not be explainable: %q", s)
		}
	}
}

// PostgreSQL 16+ plans statements with $n placeholders (EXPLAIN GENERIC_PLAN): that is all
// pg_stat_statements keeps. The same safety rules apply; ? placeholders stay refused.
func TestExplainableWithoutValues(t *testing.T) {
	ok := []string{
		"select * from orders where id = $1",
		"SELECT id, name FROM products WHERE description ILIKE $1 LIMIT $2",
		"select * from t where a = 1",
		"select '$1 is not a parameter' from t where a = $1",
	}
	bad := []string{
		"select * from t where a = ?",
		"select * from t where a = $1 for update",
		"select pg_sleep($1)",
		"select $1; drop table t",
		"with d as (delete from t where id = $1 returning *) select * from d",
		"update t set a = $1",
	}
	for _, s := range ok {
		if !ExplainableWithoutValues(s) {
			t.Errorf("should be explainable: %q", s)
		}
	}
	for _, s := range bad {
		if ExplainableWithoutValues(s) {
			t.Errorf("must not be explainable: %q", s)
		}
	}
}
