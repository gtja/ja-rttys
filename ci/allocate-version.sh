#!/usr/bin/env bash
# Atomic Git tag reservations; the SCM node needs only Bash, Git and coreutils.
set -euo pipefail

next_version() {
    local base=$1 day=$2 major minor floor patch tag candidate same_day=-1 normalized
    local semver='(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
    [[ "$base" =~ ^$semver$ ]] || { echo 'VERSION must contain major.minor.patch' >&2; return 1; }
    major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} floor=${BASH_REMATCH[3]}
    [[ "$day" =~ ^[0-9]{6}$ ]] || return 1
    # GNU date on Jenkins, BSD date when running the same tests on macOS.
    normalized=$(date -u -d "20${day:0:2}-${day:2:2}-${day:4:2}" +%y%m%d 2>/dev/null || date -j -u -f '%y%m%d' "$day" +%y%m%d 2>/dev/null) || return 1
    [[ "$normalized" == "$day" ]] || return 1
    patch=$floor
    while IFS= read -r tag; do
        if [[ "$tag" =~ ^v$semver\.([0-9]{6})$ ]]; then
            if [[ "${BASH_REMATCH[1]}" == "$major" && "${BASH_REMATCH[2]}" == "$minor" ]]; then
                candidate=${BASH_REMATCH[3]}
                (( candidate <= patch )) || patch=$candidate
                if [[ "${BASH_REMATCH[4]}" == "$day" ]] && (( candidate > same_day )); then
                    same_day=$candidate
                fi
            fi
        fi
    done
    (( same_day != patch )) || patch=$((patch + 1))
    printf 'v%s.%s.%s.%s\n' "$major" "$minor" "$patch" "$day"
}

if [[ ${1:-} == --next ]]; then
    next_version "$2" "$3"
    exit
fi

base_file=VERSION remote=origin output=out/source/version.json reserve=false commit='' run_id=''
while (( $# )); do
    case "$1" in
        --base-file) base_file=$2; shift 2 ;;
        --remote) remote=$2; shift 2 ;;
        --commit) commit=$2; shift 2 ;;
        --run-id) run_id=$2; shift 2 ;;
        --output) output=$2; shift 2 ;;
        --reserve) reserve=true; shift ;;
        *) echo "Unknown version allocator argument: $1" >&2; exit 1 ;;
    esac
done
[[ "$commit" =~ ^[0-9a-f]{40}$ && -n "$run_id" && "$run_id" != *$'\n'* && "$run_id" != *$'\r'* ]]
base=$(cat "$base_file")
# POSIX TZ works even on a minimal SCM image without a zoneinfo database.
day=$(TZ=CST-8 date +%y%m%d)
tag_object=''
for (( attempt=0; attempt<20; attempt++ )); do
    if [[ "$reserve" == true ]]; then
        tags=$(git ls-remote --tags --refs "$remote" | sed 's/.*refs\/tags\///')
    else
        tags=$(git tag --list)
    fi
    version=$(printf '%s\n' "$tags" | next_version "$base" "$day")
    if [[ "$reserve" == true ]]; then
        tag_object=$(printf 'object %s\ntype commit\ntag %s\ntagger Jenkins CI <ci@jingan.com> %s +0800\n\nCI version reservation: %s\nA reserved version is not proof of a successful image publication.\n' \
            "$commit" "$version" "$(date +%s)" "$run_id" | git mktag)
        if ! push_result=$(git push "$remote" "$tag_object:refs/tags/$version" 2>&1); then
            collision=$(git ls-remote --tags --refs "$remote" "refs/tags/$version")
            [[ -z "$collision" ]] || continue
            printf 'Unable to reserve version: %s\n' "$push_result" >&2
            exit 1
        fi
    fi
    # Escape the only free-text metadata field before writing JSON.
    escaped_id=${run_id//\\/\\\\}
    escaped_id=${escaped_id//\"/\\\"}
    escaped_id=${escaped_id//$'\t'/\\t}
    mkdir -p "$(dirname "$output")"
    tag_json=null object_json=null
    if [[ "$reserve" == true ]]; then
        tag_json="\"$version\"" object_json="\"$tag_object\""
    fi
    printf '{"version":"%s","base_version":"%s","build_date":"%s","time_zone":"Asia/Shanghai","source_commit":"%s","ci_run_id":"%s","reserved":%s,"git_tag":%s,"tag_object":%s}\n' \
        "$version" "$base" "$day" "$commit" "$escaped_id" "$reserve" "$tag_json" "$object_json" > "$output"
    printf '%s\n' "$version"
    exit
done
echo 'Too many concurrent version reservations; retry the build' >&2
exit 1
