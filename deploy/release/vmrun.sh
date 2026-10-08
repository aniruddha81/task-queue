# vmrun.sh: sourced by rollout.sh and the experiments. Reads the session's topology (written
# by Terraform to SSM) and runs commands on its VMs through AWS Run Command and Azure Run
# Command, never SSH. Needs the aws, az and jq CLIs, signed in.
export MSYS_NO_PATHCONV=1 # Git Bash on Windows would rewrite /tq/... into a file path
region=ap-south-1         # where the release record and topology live

topo=$(aws ssm get-parameter --region $region --name /tq/release/topology --query Parameter.Value --output text)
fqdn=$(jq -r .fqdn <<<"$topo")
vault=$(jq -r .vault <<<"$topo")
ops=$(jq -r .ops <<<"$topo")
vm() { jq -r --arg n "$1" ".vms[] | select(.name == \$n) | .$2" <<<"$topo"; }

# run <vm> <command>: run a shell command as root on a VM; fails if the command fails.
run() {
  local name=$1 cmd=$2 id cid status
  id=$(vm "$name" id)
  if [ "$(vm "$name" cloud)" = aws ]; then
    local r
    r=$(vm "$name" region)
    cid=$(aws ssm send-command --region "$r" --instance-ids "$id" --document-name AWS-RunShellScript \
      --parameters "$(jq -nc --arg c "$cmd" '{commands: [$c], executionTimeout: ["900"]}')" \
      --query Command.CommandId --output text)
    while :; do
      sleep 5
      status=$(aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query Status --output text 2>/dev/null || echo Pending)
      case $status in Pending | InProgress | Delayed) ;; *) break ;; esac
    done
    # The command's stdout to stdout, its stderr to stderr (each truncated at 24 KB by SSM).
    aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query StandardOutputContent --output text
    aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query StandardErrorContent --output text >&2
    [ "$status" = Success ]
  else
    # Run Command doesn't return the script's exit status, so success prints a marker.
    local out
    out=$(az vm run-command invoke -g "$(vm "$name" rg)" -n "$id" --command-id RunShellScript \
      --scripts "( $cmd ) && echo TQ-RUN-OK" --query 'value[0].message' -o tsv)
    # Its message is "Enable succeeded:", then [stdout] and [stderr] sections.
    sed -n '/^\[stdout\]$/,/^\[stderr\]$/{//!p}' <<<"$out" | grep -v '^TQ-RUN-OK$' || true
    sed -n '/^\[stderr\]$/,${//!p}' <<<"$out" >&2
    grep -q TQ-RUN-OK <<<"$out"
  fi
}
