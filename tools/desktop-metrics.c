#include <errno.h>
#include <libproc.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>

// macOS process-ledger samples. Supply every verified app/helper PID explicitly.
int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "usage: desktop-metrics seconds output.csv pid [pid ...]\n");
        return 2;
    }
    int seconds = atoi(argv[1]);
    if (seconds < 1 || seconds > 3600) return 2;
    FILE *out = fopen(argv[2], "w");
    if (!out) { perror("output"); return 1; }
    fprintf(out, "monotonic_seconds,pid,cpu_ns,physical_bytes,resident_bytes,idle_wakeups,interrupt_wakeups\n");
    for (int sample = 0; sample <= seconds; sample++) {
        struct timespec now;
        clock_gettime(CLOCK_MONOTONIC, &now);
        for (int i = 3; i < argc; i++) {
            struct rusage_info_v4 r = {0};
            int pid = atoi(argv[i]);
            if (pid <= 0 || proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&r)) {
                fprintf(stderr, "cannot sample pid %s: errno %d\n", argv[i], errno);
                fclose(out);
                return 1;
            }
            fprintf(out, "%.9f,%d,%llu,%llu,%llu,%llu,%llu\n",
                now.tv_sec + now.tv_nsec / 1e9, pid,
                (unsigned long long)(r.ri_user_time + r.ri_system_time),
                (unsigned long long)r.ri_phys_footprint,
                (unsigned long long)r.ri_resident_size,
                (unsigned long long)r.ri_pkg_idle_wkups,
                (unsigned long long)r.ri_interrupt_wkups);
        }
        fflush(out);
        if (sample < seconds) sleep(1);
    }
    fclose(out);
    return 0;
}
