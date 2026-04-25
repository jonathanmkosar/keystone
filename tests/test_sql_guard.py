import pytest

from keystone.util.sql_guard import UnsafeSQLError, assert_read_only


@pytest.mark.parametrize(
    "sql",
    [
        "SELECT 1",
        "select * from transactions where category='Coffee'",
        "  WITH x AS (SELECT 1) SELECT * FROM x",
        "SELECT 1; -- trailing comment",
    ],
)
def test_allowed(sql):
    assert_read_only(sql)


@pytest.mark.parametrize(
    "sql",
    [
        "",
        "   ",
        "INSERT INTO transactions VALUES (1)",
        "DELETE FROM transactions",
        "UPDATE accounts SET balance=0",
        "DROP TABLE accounts",
        "PRAGMA journal_mode=WAL",
        "ATTACH DATABASE 'x.db' AS x",
        "SELECT 1; DELETE FROM accounts",
        "/* hide */ DELETE FROM accounts",
        "-- comment\nDROP TABLE accounts",
        "VACUUM",
    ],
)
def test_rejected(sql):
    with pytest.raises(UnsafeSQLError):
        assert_read_only(sql)
