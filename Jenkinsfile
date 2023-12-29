pipeline {
    agent any
    stages {
        stage('Checkout') {
            steps {
                checkout([$class: 'GitSCM', branches: [[name: 'refs/heads/develop']], doGenerateSubmoduleConfigurations: false, extensions: [], submoduleCfg: [], userRemoteConfigs: [[credentialsId: '', url: 'https://github.com/signal18/replication-manager.git']]])
            }
        }
        stage('Build OSC') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def newApp = docker.build('signal18/replication-manager:2.3', '-f docker/Dockerfile .')
                        newApp.push()
                        newApp.push 'latest'
                    }
                }
            }
        }
        stage('Build PRO') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def newApp = docker.build('signal18/replication-manager:2.3-pro', '-f docker/Dockerfile.pro .')
                        newApp.push()
                    }
                }
            }
        }
    }
    post {
        failure {
            script {
                slackSend(
                    color: '#FF0000',
                    message: "Build failed! Job: `${JOB_NAME}` Build: `${BUILD_NUMBER}`",
                    tokenCredentialId: 's18-most'
                )
            }
        }
    }
}
