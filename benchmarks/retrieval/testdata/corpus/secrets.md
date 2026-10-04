---
type: Decision
title: Credential rotation
---

# Credential rotation

## Rotation overlap
Service credentials rotate with an overlap period. Issue a second credential, deploy consumers, verify adoption, then revoke the previous credential. Logs contain the credential identifier, never the secret value.

## Ротация доступа
Сервисные учётные данные меняются с периодом перекрытия. Выпустить вторую пару, обновить потребителей, проверить использование, затем отозвать предыдущую. В логах сохраняется идентификатор, секретное значение запрещено.
