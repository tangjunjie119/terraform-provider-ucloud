package ucloud

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	uerr "github.com/ucloud/ucloud-sdk-go/ucloud/error"
)

func resourceUCloudDiskAttachment() *schema.Resource {
	return &schema.Resource{
		Create: resourceUCloudDiskAttachmentCreate,
		Read:   resourceUCloudDiskAttachmentRead,
		Delete: resourceUCloudDiskAttachmentDelete,

		Schema: map[string]*schema.Schema{
			"availability_zone": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"instance_id": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"disk_id": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
		},
	}
}

func resourceUCloudDiskAttachmentCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*UCloudClient)
	conn := client.udiskconn

	instanceId := d.Get("instance_id").(string)
	diskId := d.Get("disk_id").(string)

	req := conn.NewAttachUDiskRequest()
	req.Zone = ucloud.String(d.Get("availability_zone").(string))
	req.UHostId = ucloud.String(instanceId)
	req.UDiskId = ucloud.String(diskId)

	_, err := conn.AttachUDisk(req)
	if err != nil {
		return fmt.Errorf("error in create disk attachment, %s", err)
	}

	d.SetId(fmt.Sprintf("disk#%s:uhost#%s", diskId, instanceId))

	// after create disk attachment, we need to wait it initialized
	stateConf := &resource.StateChangeConf{
		Pending:    []string{"attaching"},
		Target:     []string{"inuse"},
		Refresh:    diskAttachmentStateRefreshFunc(client, diskId),
		Timeout:    10 * time.Minute,
		Delay:      5 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	if _, err = stateConf.WaitForState(); err != nil {
		return fmt.Errorf("wait for disk attaching failed in create disk attachment %s, %s", d.Id(), err)
	}

	return resourceUCloudDiskAttachmentRead(d, meta)
}

func resourceUCloudDiskAttachmentRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*UCloudClient)

	attach, err := parseAssociationInfo(d.Id())
	if err != nil {
		return fmt.Errorf("error in parse disk attachment %s, %s", d.Id(), err)
	}

	resourceSet, err := client.describeDiskResource(attach.PrimaryId, attach.ResourceId)

	if err != nil {
		if isNotFoundError(err) {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("do %s failed in read disk attachment %s, %s", "DescribeUDisk", d.Id(), err)
	}

	d.Set("availability_zone", d.Get("availability_zone").(string))
	d.Set("instance_id", resourceSet.UHostId)
	d.Set("disk_id", resourceSet.UDiskId)

	return nil
}

func resourceUCloudDiskAttachmentDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*UCloudClient)
	conn := client.udiskconn

	attach, err := parseAssociationInfo(d.Id())
	if err != nil {
		return fmt.Errorf("error in parse disk attachment %s, %s", d.Id(), err)
	}

	req := conn.NewDetachUDiskRequest()
	req.Zone = ucloud.String(d.Get("availability_zone").(string))
	req.UDiskId = ucloud.String(attach.PrimaryId)
	req.UHostId = ucloud.String(attach.ResourceId)

	return resource.Retry(15*time.Minute, func() *resource.RetryError {
		if _, err := conn.DetachUDisk(req); err != nil {
			if uErr, ok := err.(uerr.Error); ok && uErr.Code() != 17060 {
				return resource.NonRetryableError(fmt.Errorf("error in delete disk attachment %s, %s", d.Id(), err))
			}
		}

		// after detach disk, we need to wait it completed
		stateConf := &resource.StateChangeConf{
			Pending:    []string{"detaching"},
			Target:     []string{"available"},
			Refresh:    diskAttachmentStateRefreshFunc(client, attach.PrimaryId),
			Timeout:    10 * time.Minute,
			Delay:      5 * time.Second,
			MinTimeout: 3 * time.Second,
		}

		if _, err = stateConf.WaitForState(); err != nil {
			if _, ok := err.(*resource.TimeoutError); ok {
				return resource.RetryableError(fmt.Errorf("wait for disk detach faild, in delete disk attachment %s, %s", d.Id(), err))
			}
			return resource.NonRetryableError(fmt.Errorf("wait for disk detach faild, in delete disk attachment %s, %s", d.Id(), err))
		}

		return nil
	})
}

func diskAttachmentStateRefreshFunc(client *UCloudClient, diskId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		diskSet, err := client.describeDiskById(diskId)
		if err != nil {
			return nil, "", err
		}

		return diskSet, strings.ToLower(diskSet.Status), nil
	}
}
