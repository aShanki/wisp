// PerfLog: with WISP_PERF=1, prints "NAME <ms>" to stderr for scripts/perf.sh.
#pragma once

#include <QElapsedTimer>
#include <QtGlobal>

#include <cstdio>

namespace PerfLog {

inline bool enabled()
{
    static const bool on = qEnvironmentVariableIntValue("WISP_PERF") == 1;
    return on;
}

// Milliseconds since the first call, which main() makes on entry.
inline qint64 elapsed()
{
    static const QElapsedTimer t = [] { QElapsedTimer t; t.start(); return t; }();
    return t.elapsed();
}

inline void print(const char *name, qint64 ms)
{
    if (enabled())
        std::fprintf(stderr, "%s %lld\n", name, static_cast<long long>(ms));
}

} // namespace PerfLog
