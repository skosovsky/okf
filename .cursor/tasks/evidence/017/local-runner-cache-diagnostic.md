# Local Go result-cache diagnostic

The initial local `go test ./... -timeout=30m` did not finish and is not recorded as PASS. After 32 minutes its runner had no children, used 53% CPU and about 10 GB physical memory. A bounded sample proved it was inside `runCache.tryCacheWithID → computeTestInputsID → search.InDir → EvalSymlinks → Lstat`, before execution of the next cached package.

An existing bundle test-input log in `/tmp/okf016-cache` is 3,785,529,063 bytes (about 3.53 GiB); two filesystem test-input logs are about 234 MB each. Go reads/splits the old log and resolves paths for every recorded operation before accepting a cached test result. The test binary's `-timeout` does not limit this runner cache phase. No docs traversal into GOCACHE was found.

Root stopped only this owned runner after the diagnosis to release its memory. No test failure or product cancellation defect follows from this interrupted cache lookup. The original incomplete log is retained separately from successful checks. Mandatory full suite and race acceptance comes from the clean GitHub CI run on the PR commit; targeted local affected-package, docs, JS, vet, module and client checks are successful. Future local fresh execution uses `-count=1` to bypass test-result cache while retaining build cache.

The duplicate local full race runner had no remaining test children when stopped; its precise final runner phase was not sampled. Root stopped that owned runner after the complete clean Linux race job succeeded. Neither local full runner is presented as PASS. Local affected packages executed fresh with `-count=1` all passed (docs0.988s, viewer8.611s, CLI91.899s); mandatory full normal and race outcomes are the successful CI jobs.
