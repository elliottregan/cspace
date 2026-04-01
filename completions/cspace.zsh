#compdef cspace

# Zsh completions for cspace CLI

_cspace_instances() {
    local instances
    instances=(${(f)"$(docker ps --filter 'label=cspace.instance=true' \
        --format '{{.Label "com.docker.compose.project"}}' 2>/dev/null | sort -u)"})
    _describe 'instance' instances
}

_cspace_planets() {
    local planets=(mercury venus earth mars jupiter saturn uranus neptune)
    _describe 'planet' planets
}

_cspace() {
    local -a commands
    commands=(
        'up:Create/reconnect instance and launch Claude'
        'issue:Run autonomous agent for a GitHub issue'
        'resume:Resume a previous Claude session'
        'ssh:Shell into a running instance'
        'list:List all running instances'
        'ports:Show port mappings for an instance'
        'down:Destroy an instance and its volumes'
        'warm:Pre-provision containers'
        'shared:Manage shared browser sidecars'
        'rebuild:Rebuild the container image'
        'init:Scaffold project configuration'
        'sync-context:Generate milestone context doc'
        'self-update:Update cspace to latest version'
        'version:Show version'
        'help:Show help'
    )

    if (( CURRENT == 2 )); then
        _describe 'command' commands
        return
    fi

    case "$words[2]" in
        up)
            _cspace_planets
            ;;
        ssh|ports|down)
            _cspace_instances
            ;;
        warm)
            _cspace_planets
            ;;
        shared)
            local -a actions=(up down)
            _describe 'action' actions
            ;;
        init)
            local -a flags=('--full:Copy all templates for customization')
            _describe 'flag' flags
            ;;
        issue)
            # First arg is issue number, no completion
            ;;
        resume)
            _cspace_instances
            ;;
    esac
}

_cspace "$@"
