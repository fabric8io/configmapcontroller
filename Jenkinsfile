#!/usr/bin/groovy
@Library('github.com/rawlingsj/fabric8-pipeline-library@master')
def dummy
goNode{
  container(name: 'go') {
    stage ('build binary'){
      sh "go get github.com/fabric8io/configmapcontroller"
      sh "cd /go/src/github.com/fabric8io/configmapcontroller; make"

      sh "cp -R /go/src/github.com/fabric8io/configmapcontroller/out ."
    }
  }

  container(name: 'docker') {
    def imageName = 'docker.io/fabric8/configmapcontroller:latest'
    stage ('build image'){
      sh "cd /go/src/github.com/fabric8io/configmapcontroller; docker build -t ${imageName} ."
    }

    stage ('push image'){
      sh 'pwd'
      sh 'cd ~ && pwd'
      sh 'ls -al ~/.docker'
      sh "cd /go/src/github.com/fabric8io/configmapcontroller; docker push ${imageName}"
    }
  }
}