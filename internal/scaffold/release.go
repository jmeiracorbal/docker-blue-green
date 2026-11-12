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
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@4fad31c5482e614d80589499fa3242908e705e93
    with:
      image_prefix: ${{ vars.OCI_IMAGE_PREFIX }}
      push_username: ${{ vars.OCI_PUSH_USERNAME }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
    secrets:
      GIT_REGISTRY_TOKEN: ${{ secrets.GIT_REGISTRY_TOKEN }}

  deploy:
    if: ${{ vars.DEPLOY_ENABLED == 'true' }}
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@4fad31c5482e614d80589499fa3242908e705e93
    with:
      project: ${{ vars.DEPLOY_PROJECT }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: 4fad31c5482e614d80589499fa3242908e705e93
      deploy_host: ${{ vars.DEPLOY_HOST }}
      deploy_user: ${{ vars.DEPLOY_USER }}
      deploy_port: ${{ fromJSON(vars.DEPLOY_PORT) }}
      deploy_ssh_known_hosts: ${{ vars.DEPLOY_SSH_KNOWN_HOSTS }}
    secrets:
      DEPLOY_SSH_PRIVATE_KEY: ${{ secrets.DEPLOY_SSH_PRIVATE_KEY }}
      DEPLOY_ENV: ${{ secrets.DEPLOY_ENV }}
      OCI_PULL_USERNAME: ${{ secrets.OCI_PULL_USERNAME }}
      OCI_PULL_TOKEN: ${{ secrets.OCI_PULL_TOKEN }}
`),
	}
}
