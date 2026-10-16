package launcher

func descendantsByCreation(root int, parents map[int]int, created map[int]uint64) map[int]uint64 {
	result := map[int]uint64{}
	if created[root] == 0 {
		return result
	}
	result[root] = created[root]
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for child, ppid := range parents {
			if ppid == parent && created[child] >= created[parent] && result[child] == 0 {
				result[child] = created[child]
				queue = append(queue, child)
			}
		}
	}
	return result
}
