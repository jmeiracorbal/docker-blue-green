package scaffold

// publishWorkflowFile installs a thin, version-pinned caller for the shared
// registry publishing workflow. No registry credentials are stored in the repository.
func publishWorkflowFile() File {
	return File{
		Path: ".github/workflows/release.yml",
		Data: []byte(`name: Publish OCI images

on:
  release:
    types: [published]

permissions:
  contents: read
  packages: write

jobs:
  publish:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@f68e228fe85db9393f0eb7d61565686a01be093c
    with:
      registry_url: ${{ vars.REGISTRY_URL }}
      registry_repository: ${{ vars.REGISTRY_REPOSITORY }}
      registry_username: ${{ vars.REGISTRY_USERNAME }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
    secrets:
      GIT_REGISTRY_TOKEN: ${{ secrets.GIT_REGISTRY_TOKEN }}

  deploy:
    if: ${{ vars.DEPLOY_ENABLED == 'true' }}
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@e224ce1f30776f1929755581f68a70bfe0e48ccb
    with:
      project: ${{ vars.DEPLOY_PROJECT }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: e224ce1f30776f1929755581f68a70bfe0e48ccb
      registry_url: ${{ vars.REGISTRY_URL }}
      registry_username: ${{ vars.REGISTRY_USERNAME }}
      deploy_host: ${{ vars.DEPLOY_HOST }}
      deploy_user: ${{ vars.DEPLOY_USER }}
      deploy_port: ${{ fromJSON(vars.DEPLOY_PORT) }}
      deploy_ssh_known_hosts: ${{ vars.DEPLOY_SSH_KNOWN_HOSTS }}
    secrets:
      DEPLOY_SSH_PRIVATE_KEY: ${{ secrets.DEPLOY_SSH_PRIVATE_KEY }}
      DEPLOY_ENV: ${{ secrets.DEPLOY_ENV }}
      GIT_REGISTRY_TOKEN: ${{ secrets.GIT_REGISTRY_TOKEN }}
`),
	}
}
