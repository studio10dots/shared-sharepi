<#
.SYNOPSIS
  Smoke-tests terraform/backend/ once per region (PowerShell version of
  smoke_test_regions.sh; keep both in sync).

.DESCRIPTION
  For each region, in sequence: terraform apply, confirm the backend answers
  over HTTPS, then terraform destroy — regardless of whether the apply or the
  HTTPS check succeeded. Meant to be run after a change to main.tf/variables.tf,
  to catch a region that does not actually support one of the resources this
  module creates (Cloud Run v2, Autoclass, IAM Credentials signBlob) before an
  owner hits it in docs/SETUP.md.

  -ProjectId is never your production project if you have one deployed there
  already: this creates and destroys a real bucket/service account/Cloud Run
  service/log-bucket-config in it, once per region. Use a disposable or test
  project.

  -BackendImage defaults to Google's public "hello" sample
  (us-docker.pkg.dev/cloudrun/container/hello), which needs no setup of your
  own: this only checks that the *infrastructure* deploys and answers over
  HTTPS in each region, not your backend's own logic, so a placeholder image
  is enough. Pass your own image to test with it instead; either way,
  GOOGLE_CLIENT_ID is set to a placeholder, since nothing here signs in.

  Runs in an isolated temp copy of the *.tf files, with its own fresh
  Terraform state — never in this directory, so it can never touch a real
  deployment's terraform.tfstate (gitignored, so `git status` will not show
  one even if it exists here from a real terraform apply).

.PARAMETER Codes
  Which 2-letter area codes to test (default: all 8, the same set as
  region.sh). Pass a subset to re-check only those, e.g. -Codes me,af.

.PARAMETER SkipConfirm
  Skip the "this will create and destroy real resources" prompt.

.EXAMPLE
  .\smoke_test_regions.ps1 -ProjectId my-test-project

.EXAMPLE
  .\smoke_test_regions.ps1 -ProjectId my-test-project -Codes me,af -SkipConfirm

.NOTES
  Requires: gcloud (already authenticated with billing-project access) and
  terraform, both on PATH.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ProjectId,

    [string]$BackendImage = "us-docker.pkg.dev/cloudrun/container/hello",

    [string[]]$Codes = @("ea", "ca", "au", "me", "af", "eu", "na", "sa"),

    [switch]$SkipConfirm
)

# Deliberately not "Stop": terraform/curl write to stderr for ordinary,
# non-fatal reasons (warnings, diagnostics), and in Windows PowerShell 5.1
# redirecting a native command's stderr (the *> below) wraps each line in a
# NativeCommandError and would abort the whole script on "Stop" even when the
# command's own exit code is 0. Every terraform/curl call below is checked
# explicitly through $LASTEXITCODE instead; New-Item/Copy-Item below pass
# -ErrorAction Stop individually, since a setup failure there should abort.

# Terraform writes UTF-8 (box-drawing characters in its error output, non-ASCII
# in any message). Windows PowerShell's default console encoding is the
# system's legacy code page (e.g. Shift_JIS on a Japanese install), so without
# this terraform's own output above comes out as mojibake. -no-color also
# avoids ANSI escape codes showing up as raw bytes in the log files.
$OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$env:TF_CLI_ARGS = "-no-color"

# Mirrors region.sh's region_for(); keep both in sync.
$RegionMap = [ordered]@{
    ea = "asia-northeast1"      # East Asia            - Tokyo
    ca = "asia-south1"          # Central / South Asia  - Mumbai
    au = "australia-southeast1" # Australia             - Sydney
    me = "me-central1"          # Middle East           - Doha
    af = "africa-south1"        # Africa                - Johannesburg
    eu = "europe-west1"         # Europe                - Belgium
    na = "us-central1"          # North America         - Iowa
    sa = "southamerica-east1"   # South America         - Sao Paulo
}

$GoogleClientId = "smoke-test-placeholder.apps.googleusercontent.com"

foreach ($code in $Codes) {
    if (-not $RegionMap.Contains($code)) {
        Write-Error "unknown area code `"$code`" (expected one of: $($RegionMap.Keys -join ' '))"
        exit 2
    }
}

if (-not $SkipConfirm) {
    Write-Host "This creates and destroys real resources in GCP project '$ProjectId',"
    Write-Host "once per region ($($Codes.Count) regions: $($Codes -join ', '))."
    $reply = Read-Host "Continue? [y/N]"
    if ($reply -notmatch '^[Yy]') {
        Write-Host "Aborted."
        exit 1
    }
}

$ScriptDir = $PSScriptRoot
$WorkDir = Join-Path ([System.IO.Path]::GetTempPath()) ("chamagon-smoketest-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $WorkDir -ErrorAction Stop | Out-Null
Write-Host "Working copy and logs: $WorkDir"
Copy-Item (Join-Path $ScriptDir "*.tf") -Destination $WorkDir -ErrorAction Stop

# Runs terraform with its output going straight to a log file through cmd.exe.
# PowerShell 5.1's own `*>` writes the file as UTF-16 and wraps every stderr
# line in an ErrorRecord with a stack trace; cmd's redirection keeps terraform's
# bytes (UTF-8) as they are. Returns terraform's exit code.
function Invoke-Terraform {
    param([string]$Log, [string[]]$TfArgs)
    $quoted = ($TfArgs | ForEach-Object { '"' + $_ + '"' }) -join ' '
    cmd /s /c "terraform $quoted > `"$Log`" 2>&1"
    return $LASTEXITCODE
}

# The first "Error:" line of a log, for the summary.
function Get-FirstError {
    param([string]$Log)
    $hit = Select-String -Path $Log -Pattern 'Error:' -Encoding UTF8 | Select-Object -First 1
    if ($hit) { return $hit.Line.Trim() }
    return "(no Error: line; see the log)"
}

Push-Location $WorkDir
try {
    $initLog = Join-Path $WorkDir "init.log"
    if ((Invoke-Terraform -Log $initLog -TfArgs @("init", "-input=false")) -ne 0) {
        Write-Host "terraform init failed:"
        Get-Content $initLog -Encoding UTF8 | Write-Host
        exit 1
    }

    $applyResult = @{}
    $httpResult = @{}
    $destroyResult = @{}
    $notes = @{}

    foreach ($code in $Codes) {
        $region = $RegionMap[$code]
        Write-Host ""
        Write-Host "==== $code ($region) ===="

        # A fresh bucket name per region: reusing one right after deleting it can
        # make the IAM binding that follows the bucket's creation fail with a 404
        # ("bucket does not exist") from GCS's eventual consistency, which would
        # be reported here as a region problem when it is not.
        $suffix = [System.Guid]::NewGuid().ToString("N").Substring(0, 5)
        $tfArgs = @(
            "-auto-approve", "-input=false",
            "-var=project_id=$ProjectId",
            "-var=region=$region",
            "-var=bucket_name=$ProjectId-smoke-$code-$suffix",
            # Saved into the state at apply time: a destroy with protection left on
            # fails, leaves the service running, and breaks the next region's apply.
            "-var=deletion_protection=false",
            "-var=backend_image=$BackendImage",
            "-var=google_client_id=$GoogleClientId"
        )

        $applyLog = Join-Path $WorkDir "apply_$code.log"
        $applyOk = ((Invoke-Terraform -Log $applyLog -TfArgs (@("apply") + $tfArgs)) -eq 0)
        if ($applyOk) {
            $applyResult[$code] = "ok"
        } else {
            $applyResult[$code] = "FAIL"
            $notes[$code] = Get-FirstError $applyLog
            Write-Host "apply failed: $($notes[$code])"
        }

        if ($applyOk) {
            $url = (cmd /s /c "terraform output -raw backend_url 2>nul")
            if ([string]::IsNullOrWhiteSpace($url)) {
                $httpResult[$code] = "FAIL (no backend_url output)"
            } else {
                try {
                    $resp = Invoke-WebRequest -Uri "$url/ping" -UseBasicParsing -TimeoutSec 30
                    $status = [int]$resp.StatusCode
                } catch {
                    $status = $null
                    if ($_.Exception.Response) {
                        $status = [int]$_.Exception.Response.StatusCode
                    }
                }
                if ($status -eq 200) {
                    $httpResult[$code] = "ok (200)"
                } else {
                    $shown = if ($null -ne $status) { $status } else { "no response" }
                    $httpResult[$code] = "FAIL (http $shown)"
                    Write-Host "HTTPS check got status $shown from $url/ping"
                }
            }
        } else {
            $httpResult[$code] = "skipped"
        }

        # Always destroy, whatever happened above: the whole point is leaving
        # nothing billing for after a failed region, same as a region that worked.
        $destroyLog = Join-Path $WorkDir "destroy_$code.log"
        if ((Invoke-Terraform -Log $destroyLog -TfArgs (@("destroy") + $tfArgs)) -eq 0) {
            $destroyResult[$code] = "ok"
        } else {
            $destroyResult[$code] = "FAIL"
            $notes["$code-destroy"] = "CHECK THE CONSOLE FOR LEFTOVER RESOURCES: " + (Get-FirstError $destroyLog)
            Write-Host "destroy failed: $($notes["$code-destroy"])"
        }
    }

    # One short line per region (no table: Format-Table cuts long cells with "..."),
    # then the reasons, and the same text saved next to the logs.
    $lines = @("code region               apply http         destroy")
    foreach ($code in $Codes) {
        $lines += ("{0,-4} {1,-20} {2,-5} {3,-12} {4}" -f $code, $RegionMap[$code], $applyResult[$code], $httpResult[$code], $destroyResult[$code])
    }
    $failed = @($Codes | Where-Object { $notes.ContainsKey($_) -or $notes.ContainsKey("$_-destroy") })
    if ($failed.Count -gt 0) {
        $lines += ""
        $lines += "Failures:"
        foreach ($code in $failed) {
            if ($notes.ContainsKey($code)) { $lines += "  $code apply   : $($notes[$code])" }
            if ($notes.ContainsKey("$code-destroy")) { $lines += "  $code destroy : $($notes["$code-destroy"])" }
        }
    }
    $lines += ""
    $lines += "Logs kept at: $WorkDir (delete it yourself once you are done reading them)"
    $lines += ""
    $lines += "Note: google_logging_project_bucket_config adopts the project's single"
    $lines += "built-in _Default log bucket rather than creating one; terraform destroy"
    $lines += "cannot delete that bucket itself, only stop managing its retention_days,"
    $lines += "so it is not a resource leak if that part shows up in a destroy log."
    $lines += ""
    $lines += "A Cloud Run `"quota exceeded`" on one of the later regions is likely a"
    $lines += "per-project limit on how many regions a project can start using in a short"
    $lines += "time, not that region being unsupported: re-run it alone (-Codes sa) or in"
    $lines += "another project before concluding anything."

    Write-Host ""
    Write-Host "==== summary ===="
    $lines | ForEach-Object { Write-Host $_ }
    $lines | Set-Content -Path (Join-Path $WorkDir "summary.txt") -Encoding UTF8
} finally {
    Pop-Location
}
