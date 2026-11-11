// Uses the Jenkins/105 conventions already used by ja-alarm-mqtt-kafka-bridge and defenseAI.
// Native executables are the default deliverable. Service updates are manual.
def buildPackages(boolean publish) {
    ['amd64', 'arm64'].each { arch ->
        withEnv(["TARGET_ARCH=${arch}", "PUSH_IMAGE=${publish ? '1' : '0'}"]) {
            sh 'bash ci/native-package.sh'
            if (publish) { sh 'bash ci-package.sh' }
        }
    }
    if (publish) { sh 'bash ci/publish-manifest.sh' }
}

pipeline {
    agent none
    options {
        skipDefaultCheckout(true)
        disableConcurrentBuilds()
        timestamps()
        timeout(time: 60, unit: 'MINUTES')
        buildDiscarder(logRotator(numToKeepStr: '20', artifactNumToKeepStr: '10'))
    }
    parameters {
        string(name: 'SCM_AGENT_LABEL', defaultValue: 'built-in', description: '114 源码检出节点，避免 105 直连 GitHub 不稳定')
        string(name: 'BUILD_AGENT_LABEL', defaultValue: 'ja-rttys-builder-105', description: '105 项目专用执行器；需要 Docker/Buildx、Bash、Python 3.8+，不需要宿主机 Go/Node')
        booleanParam(name: 'PUBLISH_IMAGE', defaultValue: false, description: '可选的应用镜像；默认仅交付原生程序。禁止 PR')
        choice(name: 'RTTY_ACTION', choices: ['skip', 'check', 'deploy'], description: 'check：只读验证公网 rtty SSH/服务；deploy：人工 test 更新 47.111.155.82，要求先完成 schema 迁移和部署配置')
        booleanParam(name: 'DEPLOY_DEV', defaultValue: false, description: '仅人工触发 dev，升级 105 独立开发实例；必须先初始化服务及 /etc/ja-rttys-dev/ci-deploy.json')
    }
    stages {
        stage('Prepare selected source') {
            agent { label params.SCM_AGENT_LABEL }
            steps {
                script {
                    def revision
                    retry(3) { revision = checkout scm }
                    env.PIPELINE_BRANCH = (env.BRANCH_NAME ?: revision.GIT_BRANCH ?: '').replaceFirst('^origin/', '')
                    if (!((params.RTTY_ACTION ?: 'skip') in ['skip', 'check', 'deploy'])) { error('无效 RTTY_ACTION') }
                    if ((params.PUBLISH_IMAGE || params.DEPLOY_DEV || (params.RTTY_ACTION ?: 'skip') != 'skip') && (env.CHANGE_ID || !(env.PIPELINE_BRANCH in ['master', 'dev', 'test', 'release']))) {
                        error('当前分支/PR 只执行验证和出包，不允许发布或部署')
                    }
                    if (params.DEPLOY_DEV && (env.PIPELINE_BRANCH != 'dev' || currentBuild.getBuildCauses('hudson.model.Cause$UserIdCause').isEmpty())) {
                        error('105 开发环境部署仅允许人工触发的 dev 构建')
                    }
                    if ((params.RTTY_ACTION ?: 'skip') != 'skip' && (currentBuild.getBuildCauses('hudson.model.Cause$UserIdCause').isEmpty() || !(env.PIPELINE_BRANCH in ['dev', 'test']))) {
                        error('公网 rtty 凭证仅允许人工 dev/test 构建使用')
                    }
                    if (params.RTTY_ACTION == 'deploy' && env.PIPELINE_BRANCH != 'test') { error('公网 rtty 部署仅允许人工 test 构建') }
                    env.SOURCE_COMMIT = sh(script: 'git rev-parse HEAD', returnStdout: true).trim()
                    env.CI_RUN_ID = "${env.JOB_NAME}-${env.BUILD_NUMBER}-${env.SOURCE_COMMIT}"
                    env.PUBLISH_SELECTED = (!env.CHANGE_ID && params.PUBLISH_IMAGE).toString()
                    if (params.RTTY_ACTION == 'check' && (params.PUBLISH_IMAGE || params.DEPLOY_DEV)) { error('只读检查不能与发布或部署组合') }
                    env.RESERVE_VERSION = (!env.CHANGE_ID && params.RTTY_ACTION != 'check' && (params.PUBLISH_IMAGE || env.PIPELINE_BRANCH == 'test')).toString()
                    def publicRegistry = env.PIPELINE_BRANCH in ['test', 'release']
                    env.REGISTRY_HOST = publicRegistry ? 'jingan-registry.cn-hangzhou.cr.aliyuncs.com' : 'registry.jingan.com:32008'
                    env.IMAGE_REGISTRY = "${env.REGISTRY_HOST}/${publicRegistry ? 'jingansi' : 'ja'}"
                    env.REGISTRY_CREDENTIAL = publicRegistry ? 'public-aliyun-registry-new' : 'registry-jingan-com'
                    if (env.RESERVE_VERSION == 'true') {
                        withCredentials([usernamePassword(credentialsId: 'github-pat', usernameVariable: 'SCM_USER', passwordVariable: 'SCM_PASSWORD')]) {
                            env.PACKAGE_VERSION = sh(script: '''#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
askpass=$(mktemp)
trap 'rm -f -- "$askpass"' EXIT
cat > "$askpass" <<'ASKPASS'
#!/bin/sh
case "$1" in
    *Username*) printf '%s\\n' "$SCM_USER" ;;
    *Password*) printf '%s\\n' "$SCM_PASSWORD" ;;
esac
ASKPASS
chmod 700 "$askpass"
GIT_ASKPASS="$askpass" GIT_TERMINAL_PROMPT=0 bash ci/allocate-version.sh --reserve --commit "$SOURCE_COMMIT" --run-id "$CI_RUN_ID"
''', returnStdout: true).trim()
                        }
                    } else {
                        env.PACKAGE_VERSION = sh(script: 'bash ci/allocate-version.sh --commit "$SOURCE_COMMIT" --run-id "$CI_RUN_ID"', returnStdout: true).trim()
                    }
                    if (!(env.PACKAGE_VERSION ==~ /v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{6}/)) { error('无效交付版本') }
                    currentBuild.displayName = "#${env.BUILD_NUMBER} ${env.PACKAGE_VERSION} ${env.PIPELINE_BRANCH}"
                }
                sh '''#!/usr/bin/env bash
set -euo pipefail
mkdir -p out/source
git archive --format=tar.gz -o out/source/source.tar.gz HEAD
(cd out/source && sha256sum source.tar.gz > source.sha256)
printf '%s\n' "$SOURCE_COMMIT" > out/source/commit.txt
'''
                stash name: 'selected-source', includes: 'out/source/*'
            }
        }
        stage('Build on Linux') {
            agent { label params.BUILD_AGENT_LABEL }
            stages {
                stage('Unpack exact source') {
                    steps {
                        deleteDir()
                        unstash 'selected-source'
                        sh '''#!/usr/bin/env bash
set -euo pipefail
(cd out/source && sha256sum -c source.sha256)
[[ "$(cat out/source/commit.txt)" == "$SOURCE_COMMIT" ]]
tar -xzf out/source/source.tar.gz
docker version
python3 --version
for script in ci-package.sh scripts/ci-verify.sh ci/*.sh; do bash -n "$script"; done
python3 -m unittest discover -s tests/ci -p 'test_*.py' -v
'''
                    }
                }
                stage('Read-only public rtty preflight') {
                    when { expression { (params.RTTY_ACTION ?: 'skip') != 'skip' } }
                    steps {
                        withCredentials([usernamePassword(credentialsId: 'ssh-rtty', usernameVariable: 'DEPLOY_USER', passwordVariable: 'DEPLOY_PASSWORD')]) {
                            sh 'bash ci/rtty-remote.sh check'
                        }
                    }
                }
                stage('Check internal build images') {
                    when { expression { params.RTTY_ACTION != 'check' } }
                    steps { sh 'bash ci/check-base-images.sh' }
                }
                stage('Security and end-to-end gates') {
                    when { expression { params.RTTY_ACTION != 'check' } }
                    steps { sh 'bash scripts/ci-verify.sh' }
                }
                stage('Package amd64 and arm64') {
                    when { expression { params.RTTY_ACTION != 'check' } }
                    steps {
                        script {
                            if (env.PUBLISH_SELECTED == 'true') {
                                def dockerConfig = "${pwd(tmp: true)}/registry-${env.BUILD_NUMBER}"
                                withEnv(["DOCKER_CONFIG=${dockerConfig}"]) {
                                    try {
                                        withCredentials([usernamePassword(credentialsId: env.REGISTRY_CREDENTIAL, usernameVariable: 'REGISTRY_USER', passwordVariable: 'REGISTRY_PASSWORD')]) {
                                            sh '''#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
mkdir -p "$DOCKER_CONFIG"
printf '%s' "$REGISTRY_PASSWORD" | docker login "$REGISTRY_HOST" -u "$REGISTRY_USER" --password-stdin
'''
                                            buildPackages(true)
                                        }
                                    } finally {
                                        sh 'rm -rf -- "$DOCKER_CONFIG"'
                                    }
                                }
                            } else {
                                buildPackages(false)
                            }
                        }
                    }
                }
                stage('Update public rtty native service') {
                    when { expression { params.RTTY_ACTION == 'deploy' } }
                    steps {
                        withCredentials([usernamePassword(credentialsId: 'ssh-rtty', usernameVariable: 'DEPLOY_USER', passwordVariable: 'DEPLOY_PASSWORD')]) {
                            sh 'bash ci/rtty-remote.sh deploy'
                        }
                    }
                }
                stage('Upgrade dev service on 105') {
                    when { expression { params.DEPLOY_DEV } }
                    steps {
                        script {
                            withCredentials([
                                usernamePassword(credentialsId: 'ssh-105', usernameVariable: 'DEPLOY_USER', passwordVariable: 'DEPLOY_PASSWORD')
                            ]) {
                                sh 'bash ci/deploy-dev.sh'
                            }
                        }
                    }
                }
            }
            post {
                always {
                    archiveArtifacts artifacts: 'out/source/*,out/verify/**,out/release/**/*.tar.gz,out/release/**/*.sha256,out/release/**/*.json,out/release/**/rttys,out/deployment.json,out/rtty-*.json', allowEmptyArchive: true, fingerprint: true
                }
            }
        }
    }
    post {
        always { echo "Result=${currentBuild.currentResult}; source=${env.SOURCE_COMMIT}; version=${env.PACKAGE_VERSION}" }
    }
}
