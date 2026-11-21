"""Owned-record fixture for the local Escrow FX example; never targets its ordinary DB.

Run with the escrow repository's Python environment. The worker supplies QA_RUN_ID
and grants only the QA_FIXTURE_ environment references listed in its profile.
"""
from __future__ import annotations

import asyncio
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import sys
from urllib.parse import urlparse


def configuration(offer_acceptance: bool) -> tuple[str, str, dict[str, str]]:
    run_id = os.environ["QA_RUN_ID"]
    if not re.fullmatch(r"[a-f0-9]{32}", run_id):
        raise ValueError("Expected a QA run identifier")
    database = os.environ["QA_FIXTURE_ESCROW_DATABASE_URL"].replace("postgresql+asyncpg://", "postgresql://")
    url = urlparse(database)
    if url.hostname != "127.0.0.1" or url.port != 55442 or url.path != "/escrow_qa_journey":
        raise ValueError("This example only permits the dedicated local journey database")
    roles = ("requester", "counterparty", "competitor") if offer_acceptance else ("requester", "counterparty")
    participants = {role.upper(): f"qa-{role}-{run_id}@example.com" for role in roles}
    return run_id, database, participants


async def prepare(offer_acceptance: bool) -> None:
    run_id, database, participants = configuration(offer_acceptance)
    repo = Path(os.environ["QA_FIXTURE_ESCROW_REPO"]).resolve()
    if not (repo / "app/services/auth.py").is_file():
        raise ValueError("Escrow source is unavailable")
    sys.path.insert(0, str(repo))
    os.environ.update(APP_DATABASE_URL=database.replace("postgresql://", "postgresql+asyncpg://"), APP_NOTIFICATION_PROVIDER="logging", APP_KYC_PROVIDER="local")
    from app.domain.enums import KycStatus
    from app.services.auth import AuthService
    from app.infrastructure.database.session import AsyncSessionFactory
    from app.models.user import UserModel
    from app.services._shared import utc_now
    from sqlalchemy import update

    env: dict[str, str] = {}
    for index, (role, email) in enumerate(participants.items()):
        password = secrets.token_urlsafe(24) + "A1!"
        user = await AuthService().register_user(email=email, password=password, country="NG", phone=None)
        async with AsyncSessionFactory() as session:
            verified = {"email_verified_at": utc_now()}
            if offer_acceptance:
                verified["kyc_status"] = KycStatus.VERIFIED
            await session.execute(update(UserModel).where(UserModel.id == user.id).values(**verified))
            await session.commit()
        env.update({f"QA_TEST_{role}_EMAIL": email, f"QA_TEST_{role}_PASSWORD": password})
        if not offer_acceptance:
            env[f"QA_TEST_{role}_ID"] = str(int(hashlib.sha256((run_id + role).encode()).hexdigest()[:12], 16) % 10**10).zfill(10) + str(index + 1)
    # This is private protocol output, consumed only by the worker and never logged.
    print(json.dumps({"env": env}))


async def cleanup(offer_acceptance: bool) -> None:
    _, database, participants = configuration(offer_acceptance)
    emails = list(participants.values())
    import asyncpg

    connection = await asyncpg.connect(database)
    try:
        async with connection.transaction():
            users = [row["id"] for row in await connection.fetch("SELECT id FROM users WHERE email=ANY($1::text[]) FOR UPDATE", emails)]
            if not users:
                print("{}")
                return
            requests = [row["id"] for row in await connection.fetch("SELECT id FROM exchange_requests WHERE creator_user_id=ANY($1::uuid[])", users)]
            # Refuse cleanup if another participant has interacted with owned records.
            foreign = await connection.fetchval("SELECT EXISTS(SELECT 1 FROM exchange_offers WHERE (request_id=ANY($1::uuid[]) AND NOT offer_user_id=ANY($2::uuid[])) OR (offer_user_id=ANY($2::uuid[]) AND NOT request_id=ANY($1::uuid[])))", requests, users)
            if foreign:
                raise RuntimeError("Records have an unowned participant; preserve them for review")
            await connection.execute("DELETE FROM outbox_events WHERE recipient_user_id=ANY($1::uuid[])", users)
            await connection.execute("DELETE FROM idempotency_records WHERE principal_user_id=ANY($1::uuid[])", users)
            await connection.execute("DELETE FROM trade_contracts WHERE request_id=ANY($1::uuid[])", requests)
            await connection.execute("DELETE FROM exchange_offers WHERE request_id=ANY($1::uuid[]) OR offer_user_id=ANY($2::uuid[])", requests, users)
            await connection.execute("DELETE FROM exchange_requests WHERE creator_user_id=ANY($1::uuid[])", users)
            await connection.execute("DELETE FROM kyc_verifications WHERE user_id=ANY($1::uuid[])", users)
            await connection.execute("DELETE FROM account_audit_events WHERE subject_user_id=ANY($1::uuid[]) AND actor_user_id=ANY($1::uuid[])", users)
            await connection.execute("DELETE FROM users WHERE id=ANY($1::uuid[])", users)
        print("{}")
    finally:
        await connection.close()


if __name__ == "__main__":
    args = sys.argv[1:]
    if args not in (["prepare"], ["cleanup"], ["prepare", "offers"], ["cleanup", "offers"]):
        raise SystemExit("Use prepare or cleanup, optionally followed by offers")
    command = prepare if args[0] == "prepare" else cleanup
    asyncio.run(command(offer_acceptance=args[1:] == ["offers"]))
