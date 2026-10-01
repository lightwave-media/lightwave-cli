# Rendered by `lw cron sync` from lightwave-core origin/main:src/schemas/workflows/scheduled_jobs. Do not edit: change the stamp
# in lightwave-core and re-run `lw cron sync --out <this checkout>`.
{ ... }:
{
  launchd.user.agents."com.lightwave.cron.nightly_audit".serviceConfig = {
    Label = "com.lightwave.cron.nightly_audit";
    ProgramArguments = [ "/home/u/.local/bin/lw" "config" "exec" "--only" "GITHUB_PACKAGES_READ_TOKEN,NULLTICKETS_API_TOKEN" "--" "/home/u/.local/bin/lw" "cron" "run" "nightly_audit" ];
    EnvironmentVariables = {
      AWS_PROFILE = "lightwave-agent";
      LW_AGENT_ID = "v_scrum-manager";
      PATH = "/home/u/.local/bin:/usr/bin:/bin";
    };
    StartCalendarInterval = [
      { Hour = 3; Minute = 0; }
    ];
    StandardOutPath = "/home/u/.lightwave/observability/launchd/com.lightwave.cron.nightly_audit.stdout.log";
    StandardErrorPath = "/home/u/.lightwave/observability/launchd/com.lightwave.cron.nightly_audit.stderr.log";
  };
}
