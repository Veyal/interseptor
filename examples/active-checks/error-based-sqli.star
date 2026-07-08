# Error-based SQL injection (custom active check).
#
# Sends a single quote into the current injection point and looks for a database
# error string in the response — a classic, high-signal SQLi confirmation.
#
# `probe(payload)` sends a REAL mutated request (recorded in History, session-auth
# applied, counts against the run's request budget). `point` tells you what you're
# injecting into; `baseline` is the un-mutated response for comparison.
#
# Copy into ~/.interseptor/active-checks/ and arm & run an active scan.

def check(point, baseline, probe):
    r = probe("'")
    if re_search("(?i)(SQL syntax|ORA-|SQLSTATE\\[|sqlite3\\.OperationalError|pg_query|unclosed quotation mark|System\\.Data\\.SqlClient\\.SqlException)", r.body):
        return [finding(
            "High",
            "Error-based SQL injection (custom)",
            detail="Injecting a single quote into " + point.kind + " parameter `" + point.name + "` triggered a database error in the response, indicating the value reaches a SQL query unparameterized.",
            evidence=r.body[:120],
            fix="Use parameterized queries / prepared statements for all database access; never concatenate user input into SQL.",
        )]
    return []
