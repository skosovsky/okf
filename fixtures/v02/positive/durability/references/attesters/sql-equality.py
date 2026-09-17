# Inert reference example: the sanctioned SQL digest is pinned to the captured
# computation bytes. This file documents the attestation rule; the OKF toolkit
# does not execute attester resources.
import hashlib

SANCTIONED_SQL_SHA256 = "0f93166f23e8b588ec0d8a12e6c80c8b018bc449a7ec247b1812caa7b142ea3d"


def attest(receipt: dict) -> bool:
    executed_sql = receipt.get("executed_sql")
    if not isinstance(executed_sql, str):
        return False
    return hashlib.sha256(executed_sql.encode()).hexdigest() == SANCTIONED_SQL_SHA256
