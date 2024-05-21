pipeline {
    agent any
    stages {
        stage('Checkout') {
            steps {
                checkout([$class: 'GitSCM', branches: [[name: 'refs/heads/develop']], doGenerateSubmoduleConfigurations: false, extensions: [], submoduleCfg: [], userRemoteConfigs: [[credentialsId: '', url: 'https://github.com/signal18/replication-manager.git']]])
            }
        }
        stage('Build OSC') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:2.3', '-f docker/Dockerfile .')
                        Image.push()
                        Image.push('latest')
                        Image.push(env.TAG_NAME)
                    }
                }
            }
        }
        stage('Build PRO') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:2.3-pro', '-f docker/Dockerfile.pro .')
                        Image.push()
                        Image.push(env.TAG_NAME+'-pro')
                    }
                }
            }
        }
        stage('Build DEV') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:2.3-dev', '-f docker/Dockerfile.dev .')
                        Image.push()
                        Image.push('dev')
                        Image.push(env.TAG_NAME+'-dev')
                    }
                }
            }
        }
    }
    post {
        failure {
            script {
                mattermostSend(
                    color: '#FF0000',
                    message: "Build failed! Job: `${JOB_NAME}` Build: `${BUILD_NUMBER}` (<${env.BUILD_URL}|Open>)"
                )
            }
        }
        success {
            script {
                sh 'docker system prune --force'
            }
        }
    }
}
